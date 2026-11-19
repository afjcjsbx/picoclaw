package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/constants"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

// maxOutputBufferSize caps the rolling output retained in memory per session.
// The full stream spills to an artifact once it overflows, so a low-resource
// host keeps only a small tail per background process.
const maxOutputBufferSize = 100 * 1024 // 100KB

// maxForegroundTimeout caps how long a single foreground exec call may block.
// Requests above it are promoted to a tracked background session instead of
// being clamped or rejected.
const maxForegroundTimeout = config.MaxExecForegroundTimeoutSeconds * time.Second

var (
	globalSessionManager = NewSessionManager()
	sessionManagerMu     sync.RWMutex
)

func getSessionManager() *SessionManager {
	sessionManagerMu.RLock()
	defer sessionManagerMu.RUnlock()
	return globalSessionManager
}

type ExecTool struct {
	workingDir    string
	timeout       time.Duration
	foregroundCap time.Duration
	// denyPatterns are the optional text patterns of the default guard. Only
	// Windows has any: the Unix constructs are checked on the parsed command.
	denyPatterns []*regexp.Regexp
	// blockOpaque enables the default guard for constructs the AST cannot
	// inspect (see opaqueFallbackPatterns).
	blockOpaque          bool
	hardlineDenyPatterns []*regexp.Regexp
	customDenyPatterns   []*regexp.Regexp
	customAllowPatterns  []*regexp.Regexp
	// allowedPathPatterns are paths exec may read or modify. exec can mutate
	// any path it receives, so callers must pass write grants, not a read-only
	// allowlist.
	allowedPathPatterns  []*regexp.Regexp
	restrictToWorkspace  bool
	allowRemote          bool
	maxOutputChars       int
	maxInlineResultChars int
	// baseEnv is the lookup view for the command guard (HOME, CDPATH).
	// baseEnvList is the same data pre-rendered and sorted for runEnv, so a
	// command does not rebuild and sort the environment on every call.
	baseEnv        map[string]string
	baseEnvList    []string
	state          *execStateStore
	sessionManager *SessionManager
	riskGuard      *shellguard.Guard
}

var _ AsyncExecutor = (*ExecTool)(nil)

// NewExecToolWithConfig builds an exec tool. allowPaths optionally grants
// access to paths outside the workspace; exec can mutate those paths, so
// callers must pass write grants rather than a read-only allowlist.
func NewExecToolWithConfig(
	workingDir string,
	restrict bool,
	cfg *config.Config,
	allowPaths ...[]*regexp.Regexp,
) (*ExecTool, error) {
	// Fail closed when no config is supplied: the default guard is on and
	// remote exec is off. Callers that need remote exec must opt in explicitly
	// through cfg.Tools.Exec.AllowRemote.
	execConfig := config.ExecConfig{EnableDenyPatterns: true}
	maxInlineResultChars := 0
	if cfg != nil {
		execConfig = cfg.Tools.Exec
		maxInlineResultChars = cfg.Tools.GetMaxInlineResultChars()
	}
	enableDefaultGuard := execConfig.EnableDenyPatterns

	var allowedPathPatterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		allowedPathPatterns = allowPaths[0]
	}

	if !enableDefaultGuard {
		// Only the optional built-in default set is disabled here. The
		// hardline floor and operator-defined custom deny rules still apply.
		logger.WarnCF(
			"tools",
			"default deny patterns are disabled; hardline and custom deny patterns still apply",
			nil,
		)
	}
	var customDenyPatterns []*regexp.Regexp
	if len(execConfig.CustomDenyPatterns) > 0 {
		logger.InfoCF("tools", "using custom deny patterns", map[string]any{
			"patterns": execConfig.CustomDenyPatterns,
		})
		var err error
		customDenyPatterns, err = compilePatterns(execConfig.CustomDenyPatterns, regexp.Compile, "custom deny")
		if err != nil {
			return nil, err
		}
	}
	customAllowPatterns, err := compilePatterns(
		execConfig.CustomAllowPatterns, compileFullMatchPattern, "custom allow",
	)
	if err != nil {
		return nil, err
	}

	var denyPatterns []*regexp.Regexp
	if enableDefaultGuard {
		denyPatterns = platformDenyPatterns()
	}

	riskGuard, err := newRiskGuard(execConfig.RiskThreshold, enableDefaultGuard)
	if err != nil {
		return nil, err
	}
	if cfg != nil {
		if _, err := config.ParseExecUnknownCommandPolicy(cfg.Tools.Exec.UnknownCommandPolicy); err != nil {
			return nil, err
		}
	}

	hardlinePatterns := make([]*regexp.Regexp, 0, len(hardlineDenyPatterns)+8)
	hardlinePatterns = append(hardlinePatterns, hardlineDenyPatterns...)
	hardlinePatterns = append(hardlinePatterns, selfProtectionHardlinePatterns()...)

	baseEnv := newExecEnv(workingDir, execConfig.EnvSet, execConfig.EnvAllowlist)
	return &ExecTool{
		workingDir:           workingDir,
		timeout:              time.Duration(execConfig.GetTimeoutSeconds()) * time.Second,
		foregroundCap:        maxForegroundTimeout,
		denyPatterns:         denyPatterns,
		blockOpaque:          enableDefaultGuard,
		hardlineDenyPatterns: hardlinePatterns,
		customDenyPatterns:   customDenyPatterns,
		customAllowPatterns:  customAllowPatterns,
		allowedPathPatterns:  allowedPathPatterns,
		restrictToWorkspace:  restrict,
		allowRemote:          execConfig.AllowRemote,
		maxOutputChars:       execConfig.GetMaxOutputChars(),
		maxInlineResultChars: maxInlineResultChars,
		baseEnv:              baseEnv,
		baseEnvList:          buildEnvList(baseEnv),
		state:                newExecStateStore(),
		sessionManager:       getSessionManager(),
		riskGuard:            riskGuard,
	}, nil
}

func (t *ExecTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	return t.execute(ctx, args, false, nil)
}

// executeScheduled runs a command for an already-authorized cron job. CronTool
// has already checked tools.cron.command_allowed_remotes; this bypasses only
// the interactive remote-channel gate for a run action. Every command guard
// (deny patterns, hardline rules, risk checks, and allow patterns) still
// applies.
func (t *ExecTool) executeScheduled(ctx context.Context, args map[string]any) *ToolResult {
	return t.execute(ctx, args, true, nil)
}

func (t *ExecTool) execute(ctx context.Context, args map[string]any, scheduled bool, cb AsyncCallback) *ToolResult {
	action, _ := args["action"].(string)
	if action == "" {
		return ErrorResult("action is required")
	}

	// GHSA-pv8c-p6jf-3fpp: block the whole exec tool from remote channels
	// (e.g. Telegram webhooks) unless explicitly opted-in via config. This
	// applies to every action, not only run: list/read/poll/wait can leak
	// session output and write/kill/send-keys can mutate internal sessions.
	// Fail-closed: empty channel = blocked. The channel must come from the
	// trusted tool context, never from model-controlled arguments. Scheduled
	// cron command jobs are authorized by their own allowlist and reach this
	// tool through executeScheduled; only their run action skips this gate.
	scheduledRun := scheduled && action == "run"
	if !t.allowRemote && !scheduledRun && !constants.IsInternalChannel(strings.TrimSpace(ToolChannel(ctx))) {
		return ErrorResult("exec is restricted to internal channels")
	}

	switch action {
	case "run":
		return t.executeRun(ctx, args, cb)
	case "list":
		return t.executeList(ctx)
	case "poll":
		return t.executePoll(ctx, args)
	case "wait":
		return t.executeWait(ctx, args)
	case "read":
		return t.executeRead(ctx, args)
	case "write":
		return t.executeWrite(ctx, args)
	case "kill":
		return t.executeKill(ctx, args)
	case "send-keys":
		return t.executeSendKeys(ctx, args)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s", action))
	}
}

// ExecuteAsync installs the completion callback for runs that return a
// session, including foreground calls promoted to background. The callback is
// installed before the run returns, so a process that exits immediately cannot
// lose its notification. The callback is also stored with notify=false so
// repeated silent poll/wait calls can promote the session later.
func (t *ExecTool) ExecuteAsync(
	ctx context.Context,
	args map[string]any,
	cb AsyncCallback,
) *ToolResult {
	return t.execute(ctx, args, false, cb)
}

func (t *ExecTool) completionResult(session *ProcessSession) *ToolResult {
	status := session.GetStatus()
	output := session.Tail(sessionOutputChars)
	msg := fmt.Sprintf(
		"Background command completed.\nsessionId: %s\ncommand: %s\nstatus: %s\nexitCode: %d",
		session.ID, session.Command, status, session.GetExitCode(),
	)
	if output != "" {
		msg += "\n\nOutput tail:\n" + output
	}
	return NewToolResult(msg)
}

func (t *ExecTool) SetTimeout(timeout time.Duration) {
	t.timeout = timeout
}

// DisableBackgroundPromotion makes foreground runs keep their configured
// timeout instead of being promoted to a tracked background session past the
// foreground cap. Callers that consume the output in the same call need it.
func (t *ExecTool) DisableBackgroundPromotion() {
	t.foregroundCap = 0
}
