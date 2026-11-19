package tools

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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

var (
	// opaqueFallbackPatterns are text versions of the shell constructs that
	// hide a command from the AST: command substitution, backticks,
	// pipe-to-shell and shell heredocs. shellguard.Analysis reports these
	// precisely from the parse, so the patterns only run when the command does
	// not parse, and on Windows where the POSIX parse does not apply. Command
	// names such as sudo, chmod, git push or docker run are classified by
	// risk.go.
	opaqueFallbackPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\$\([^)]+\)`),
		regexp.MustCompile("`[^`]+`"),
		regexp.MustCompile(`\|&?\s*(?:sh|bash|zsh|dash|ksh|fish|csh|tcsh)\b`),
		regexp.MustCompile(`\b(?:sh|bash|zsh|dash|ksh|fish|csh|tcsh)\b[^|;&\n]*<<`),
	}

	// hardlineDenyPatterns are catastrophic commands that stay blocked even when
	// enable_deny_patterns is false. Unlike the optional built-in default set,
	// they cannot be disabled through configuration or exempted by custom allow
	// patterns.
	hardlineDenyPatterns = []*regexp.Regexp{
		// Shell fork bomb.
		regexp.MustCompile(`:\(\)\s*\{.*\};\s*:`),
		// Filesystem creation and disk partitioning tools.
		regexp.MustCompile(`\bmkfs(?:\.\w+)?\b`),
		regexp.MustCompile(`\bdiskpart\b`),
		regexp.MustCompile(`(^|[^-\w])\bformat\s+(?:/[^\s]+\s+)*[a-z]:`),
		// Raw disk access.
		regexp.MustCompile(`\bdd\s+if=`),
		regexp.MustCompile(
			`>\s*/dev/(?:sd[a-z]|hd[a-z]|vd[a-z]|xvd[a-z]|nvme\d|mmcblk\d|loop\d|dm-\d|md\d|sr\d|nbd\d)`,
		),
		// Recursive deletion of the filesystem root.
		regexp.MustCompile(`\brm\s+(?:-{1,2}[a-z-]*\s+)*/(?:[\s*/.]|$)`),
		// Recursive deletion of the directories that hold the OS, system
		// binaries, user homes and the root account. Subdirectories remain
		// governed by the risk classifier, so rm -rf /home/alice/build is not
		// caught here.
		regexp.MustCompile(
			`\brm\s+(?:-{1,2}[a-z-]*\s+)*/+` +
				`(?:etc|usr|home|root|bin|sbin|lib|lib64|boot|var|opt|srv|sys|proc|dev)` +
				`(?:/\*)?/?` + `(?:[\s;&|<>()]|$)`,
		),
		// Sending SIGHUP to every process, or SIGKILL to PID 1.
		regexp.MustCompile(`\bkill\s+(?:--\s+)?-1(?:[\s;&|<>()]|$)`),
		regexp.MustCompile(`\bkill\s+(?:-9|-KILL|-SIGKILL)\s+1(?:[\s;&|<>()]|$)`),
		// Host power state changes.
		regexp.MustCompile(`\b(?:shutdown|reboot|poweroff)\b`),
	}

	// windowsDenyPatterns contains the Windows-only regex floor. The AST risk
	// classifier is POSIX-only and skipped on Windows, so the generic command
	// patterns that risk.go covers on Unix stay here, next to the PowerShell
	// specific patterns.
	windowsDenyPatterns = []*regexp.Regexp{
		// Generic command risks that risk.go classifies on Unix.
		regexp.MustCompile(`\bdel\s+/[fq]\b`),
		regexp.MustCompile(`\brmdir\s+/s\b`),
		regexp.MustCompile(`\bnpm\s+install\s+-g\b`),
		regexp.MustCompile(`\bpip\s+install\s+--user\b`),
		regexp.MustCompile(`\bdocker\s+(?:run|exec)\b`),
		regexp.MustCompile(`\bgit\s+push\b`),
		regexp.MustCompile(`\bgit\s+force\b`),
		regexp.MustCompile(`\bssh\b.*@`),
		regexp.MustCompile(`\b(?:kill|pkill|killall)\b`),
		// The AST risk classifier is POSIX-only and skipped on Windows, so keep
		// a regex floor for recursive/forced rm there.
		regexp.MustCompile(`\brm\s+-[rf]{1,2}\b`),
		// [Text.Encoding] used to construct command strings at runtime.
		// Matches [Text.Encoding] and [System.Text.Encoding] variants.
		regexp.MustCompile(`\[(?:\w+\.)?text\.encoding\]`),
		// PowerShell -EncodedCommand flag (base64-encoded command) and all short forms.
		// Matches: -e, -ec, -enc, -en, -EncodedCommand (all with space prefix)
		regexp.MustCompile(` -e(?:$|\s)| -ec(?:$|\s)| -enc(?:$|\s)| -en(?:$|\s)| -encodedcommand\b`),
		// .GetString called on byte array to decode commands.
		regexp.MustCompile(`\.getstring\s*\(\s*\[byte\[\]`),
		// FromBase64String used in command construction chain.
		regexp.MustCompile(`frombase64string\(`),
		// PowerShell variable holding byte array used in GetString.
		regexp.MustCompile(`\$[a-zA-Z_]\w*\s*=\s*\[byte\[\]`),
		// Unicode escape sequences that could be used to construct commands.
		// Matches \uXXXX format used to represent characters like i = "i"
		regexp.MustCompile(`\\u[0-9a-fA-F]{4}`),
		// Recursive deletion through Remove-Item or its PowerShell aliases.
		// The flag pattern accepts unambiguous abbreviations such as -Rec.
		regexp.MustCompile(`\b(?:remove-item|rm|ri|del|erase|rd|rmdir)\b[^|;&\r\n]*\s-r(?:ec(?:ur(?:se)?)?)?\b`),
		// Disk management cmdlets that clear or reformat a volume.
		regexp.MustCompile(`\b(?:clear-disk|format-volume)\b`),
		// Deleting shadow copies destroys local backup and recovery points.
		regexp.MustCompile(`\bvssadmin(?:\.exe)?\b[^|;&\r\n]*\sdelete\s+shadows\b`),
		// Boot configuration changes can disable recovery or boot integrity.
		regexp.MustCompile(`\bbcdedit(?:\.exe)?\b[^|;&\r\n]*\s/set\b`),
		// Registry key deletion.
		regexp.MustCompile(`\breg(?:\.exe)?\s+delete\b`),
		// Granting Everyone access weakens ACLs on the target path.
		regexp.MustCompile(`\bicacls(?:\.exe)?\b[^|;&\r\n]*\s/grant\b[^|;&\r\n]*(?:\beveryone\b|\*s-1-1-0)`),
		// Download-and-execute pipelines through PowerShell web cmdlets.
		regexp.MustCompile(
			`\b(?:iwr|irm|invoke-webrequest|invoke-restmethod|curl|wget)\b` +
				`[^;&\r\n]*\|\s*(?:iex|invoke-expression)\b`,
		),
		// Forced process termination.
		regexp.MustCompile(`\btaskkill(?:\.exe)?\b[^|;&\r\n]*\s/f\b`),
		regexp.MustCompile(`\b(?:stop-process|spps)\b[^|;&\r\n]*\s-f(?:o(?:r(?:ce)?)?)?\b`),
	}

	// pathTraversalPattern catches parent-directory syntax that the Windows
	// path scanner and dotted forms such as .../ still need byte-level
	// matching for.
	pathTraversalPattern = regexp.MustCompile(`\.\.(?:[\\/]\.\.)*[\\/]`)

	// safePaths are kernel pseudo-devices that are always safe to reference in
	// commands, regardless of workspace restriction. They contain no user data
	// and cannot cause destructive writes.
	safePaths = map[string]bool{
		"/dev/null":    true,
		"/dev/zero":    true,
		"/dev/random":  true,
		"/dev/urandom": true,
		"/dev/stdin":   true,
		"/dev/stdout":  true,
		"/dev/stderr":  true,
	}
)

// selfProtectionDeleteVerbs matches commands that remove files or directories,
// including the PowerShell aliases of Remove-Item.
const selfProtectionDeleteVerbs = `\b(?:rm|unlink|shred|truncate|del|erase|remove-item|ri|rd|rmdir)\b`

// selfProtectionHardlinePatterns returns hardline patterns that keep the
// running PicoClaw instance from terminating or deleting itself. The patterns
// are built per tool so they reflect the current executable, state directory
// and service name. They are always enforced, including when default deny
// patterns are disabled or a broad custom allow rule is configured.
func selfProtectionHardlinePatterns() []*regexp.Regexp {
	patterns := []*regexp.Regexp{
		// Killing the agent by process name.
		regexp.MustCompile(`\b(?:pkill|killall)\b[^;|&\n]*\bpicoclaw(?:\.exe)?\b`),
		// Killing the agent after resolving its PID.
		regexp.MustCompile(`\bkill\b[^;|&\n]*(?:pgrep|pidof)\b[^;|&\n]*\bpicoclaw(?:\.exe)?\b`),
		// Killing the agent by process name on Windows.
		regexp.MustCompile(`\b(?:stop-process|spps)\b[^;|&\n]*(?:-name\s+)?picoclaw(?:\.exe)?\b`),
		regexp.MustCompile(`\btaskkill(?:\.exe)?\b[^;|&\n]*\bpicoclaw(?:\.exe)?\b`),
		// Stopping the agent service.
		regexp.MustCompile(`\bsystemctl\b[^;|&\n]*\b(?:stop|kill)\b[^;|&\n]*\bpicoclaw(?:\.service)?\b`),
		// Removing the default state directory through shell expansion.
		regexp.MustCompile(
			selfProtectionDeleteVerbs +
				`[^;|&\n]*(?:~|\$home|\$\{home\})["']?/\.picoclaw\b`,
		),
		// Removing the executable through command lookup.
		regexp.MustCompile(
			selfProtectionDeleteVerbs +
				`[^;|&\n]*(?:which|command\s+-v|type\s+-p)\s+picoclaw(?:\.exe)?\b`,
		),
		// Removing the state directory or executable through the variables
		// exported to every command.
		regexp.MustCompile(
			selfProtectionDeleteVerbs +
				`[^;|&\n]*\$(?:picoclaw_home|picoclaw_exe|\{picoclaw_home\}|\{picoclaw_exe\})` +
				`(?:[^a-z0-9_]|$)`,
		),
		// Killing the agent through the exported executable variable.
		regexp.MustCompile(
			`\b(?:pkill|killall)\b[^;|&\n]*\$(?:picoclaw_exe|\{picoclaw_exe\})(?:[^a-z0-9_]|$)`,
		),
		// Stopping the agent through the exported service name.
		regexp.MustCompile(
			`\bsystemctl\b[^;|&\n]*\b(?:stop|kill)\b` +
				`[^;|&\n]*\$(?:picoclaw_service_name|\{picoclaw_service_name\})(?:[^a-z0-9_]|$)`,
		),
	}

	serviceName := strings.ToLower(strings.TrimSpace(os.Getenv(config.EnvServiceName)))
	if serviceName == "" {
		serviceName = "picoclaw"
	}
	if serviceName != "picoclaw" {
		quoted := regexp.QuoteMeta(serviceName)
		patterns = append(patterns,
			regexp.MustCompile(`\b(?:pkill|killall)\b[^;|&\n]*\b`+quoted+`\b`),
			regexp.MustCompile(
				`\bsystemctl\b[^;|&\n]*\b(?:stop|kill)\b[^;|&\n]*\b`+quoted+`(?:\.service)?\b`,
			),
		)
	}

	var protectedPaths []string
	if home := config.GetHome(); home != "" && home != "." {
		protectedPaths = append(protectedPaths, home)
		if resolved, err := filepath.EvalSymlinks(home); err == nil && resolved != "" {
			protectedPaths = append(protectedPaths, resolved)
		}
	}
	if executable, err := os.Executable(); err == nil && executable != "" {
		protectedPaths = append(protectedPaths, executable)
		if resolved, err := filepath.EvalSymlinks(executable); err == nil && resolved != "" {
			protectedPaths = append(protectedPaths, resolved)
		}
	}

	seen := make(map[string]struct{}, len(protectedPaths))
	for _, path := range protectedPaths {
		path = strings.TrimSpace(path)
		if path == "" || path == "." {
			continue
		}
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
		if path == string(filepath.Separator) || filepath.Dir(path) == path {
			continue
		}
		for _, variant := range protectedPathForms(path) {
			key := strings.ToLower(variant)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			quoted := regexp.QuoteMeta(key)
			patterns = append(patterns, regexp.MustCompile(
				selfProtectionDeleteVerbs+`[^;|&\n]*`+quoted+`(?:[^a-z0-9_]|$)`,
			))
		}
	}
	return patterns
}

// compilePatterns compiles each pattern, naming the failing one in the error.
func compilePatterns(
	patterns []string,
	compile func(string) (*regexp.Regexp, error),
	label string,
) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		re, err := compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid %s pattern %q: %w", label, pattern, err)
		}
		compiled = append(compiled, re)
	}
	return compiled, nil
}

// compileFullMatchPattern anchors an operator-provided allow pattern so it must
// match the whole command. A bare "git" pattern no longer exempts
// "git push --force".
func compileFullMatchPattern(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(`^(?:` + pattern + `)$`)
}

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

func (t *ExecTool) Name() string {
	return "exec"
}

// execDescription only depends on runtime.GOOS, so build it once instead of on every call.
var execDescription = buildExecDescription()

func (t *ExecTool) Description() string {
	return execDescription
}

func buildExecDescription() string {
	return execDescriptionFor(runtime.GOOS)
}

// execDescriptionFor renders the description for one OS. Windows gets its own
// wording because PowerShell has no && and no trailing-& backgrounding, so
// teaching those forms would send the model into rejected commands.
func execDescriptionFor(goos string) string {
	osName := goos
	shell := "sh"
	sessionGuidance := "Use `pty=true` for interactive programs. Trailing `&` implies background. " +
		"Never detach untracked processes via `nohup`, `setsid` or `disown`."
	chainGuidance := "chain dependent ones with `&&`."
	if goos == "windows" {
		shell = "PowerShell"
		sessionGuidance = "PTY mode is not supported on Windows. Do not start untracked background processes."
		chainGuidance = "PowerShell has no `&&`, so use `cmd1; if ($?) { cmd2 }`."
	} else if goos == "darwin" {
		osName = "macOS"
	}

	capSeconds := int64(maxForegroundTimeout / time.Second)
	return strings.Join([]string{
		fmt.Sprintf("Executes shell commands with PicoClaw safety guards. OS: %s; shell: %s.", osName, shell),
		"",
		"Each foreground call starts a fresh shell in the remembered cwd (use `cwd`, not `cd`). Prefer `list_dir`, `search_files`, `read_file`, `edit_file`, `write_file` over shell equivalents.",
		"",
		"Environment is sanitized; only safe and allowlisted variables are inherited. Set extras inline (`FOO=bar cmd`).",
		"",
		"Results contain `[exec]` metadata (exitCode, timedOut, truncated, durationMs, error), then stdout/stderr with ANSI escape sequences removed. Null exitCode means signal termination; 128+N may also indicate signals (137=SIGKILL, 143=SIGTERM). Treat these as failures. Some non-zero codes are normal (grep:1=no match; diff:1=differences).",
		"",
		"Truncated output is saved as an artifact; use `read_file` on its path.",
		"",
		fmt.Sprintf(
			"`run` requires `command`. Timeout is in seconds; foreground max is %ds. "+
				"`timeout=0` or above %ds starts a tracked background session. "+
				"`timeout` and `background=true` are incompatible.",
			capSeconds, capSeconds,
		),
		"",
		fmt.Sprintf(
			"Use background only when needed; it returns `sessionId`. "+
				"Use `list`, `poll`, `wait` (preferred; `notify=true` alerts on completion), "+
				"`read` (byte offset/limit), `write` (stdin), `send-keys` or `kill`. %s",
			sessionGuidance,
		),
		"",
		"Run independent commands separately; " + chainGuidance + " Avoid newline-separated commands.",
	}, "\n")
}

var execActions = []string{"run", "list", "poll", "wait", "read", "write", "kill", "send-keys"}

func (t *ExecTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        execActions,
				"description": "Action: run (execute command), list (show sessions), poll (check status, returns immediately), wait (block until a session exits or timeout), read (get output), write (send input), kill (terminate), send-keys (send keys to PTY)",
			},
			"command": map[string]any{
				"type":        "string",
				"description": "Shell command to execute (required for run)",
			},
			"sessionId": map[string]any{
				"type":        "string",
				"description": "Session ID (required for poll/wait/read/write/kill/send-keys)",
			},
			"keys": map[string]any{
				"type":        "string",
				"description": "Key names for send-keys: up, down, left, right, enter, tab, escape, backspace, ctrl-c, ctrl-d, home, end, pageup, pagedown, f1-f12",
			},
			"data": map[string]any{
				"type":        "string",
				"description": "Data to write to stdin (required for write)",
			},
			"background": map[string]any{
				"type":        "boolean",
				"description": "Run in background immediately",
			},
			"pty": map[string]any{
				"type":        "boolean",
				"description": "Run in a pseudo-terminal (PTY) when available",
			},
			"cwd": map[string]any{
				"type":        "string",
				"description": "Working directory for the command. The last explicit value is remembered for later calls in the same conversation; omit cwd to reuse it. Defaults to the agent workspace when no value is remembered.",
			},
			"timeout": map[string]any{
				"type": "integer",
				"description": fmt.Sprintf(
					"Optional timeout in seconds for foreground commands; overrides the configured timeout. Foreground runs are capped at %ds: 0 (no timeout) and values above the cap start a tracked background session instead. Not supported with background=true. For wait: maximum seconds to block (default and cap: the configured timeout).",
					int64(maxForegroundTimeout/time.Second),
				),
			},
			"notify": map[string]any{
				"type":        "boolean",
				"description": "With background=true or a run promoted to background, send a completion message when the process exits. Use it for jobs with a defined end; do not poll or wait in a loop.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "With read: absolute byte offset into the retained session log. Defaults to the start of the most recent limit bytes; use 0 for the oldest retained byte.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "With read: maximum bytes to return. Defaults to the configured foreground output cap.",
			},
		},
		"required": []string{"action"},
	}
}

// boolArg reads a boolean flag. The schema declares these fields as booleans,
// but the string branch keeps calls working for models that still emit the
// "true"/"false" form the schema used to advertise.
func boolArg(args map[string]any, key string) bool {
	switch v := args[key].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

// numberArg reads an optional integer JSON number. JSON tool arguments decode
// every number as float64, so a fractional value is rejected. valid is false
// only when the key is present and is not an integer >= min.
func numberArg(args map[string]any, key string, minimum float64) (value float64, provided, valid bool) {
	raw, ok := args[key]
	if !ok {
		return 0, false, true
	}
	value, ok = raw.(float64)
	if !ok || value != math.Trunc(value) || value < minimum {
		return 0, true, false
	}
	return value, true, true
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
