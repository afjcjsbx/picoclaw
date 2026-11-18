package tools

import (
	"context"
	"errors"
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
	fstools "github.com/sipeed/picoclaw/pkg/tools/fs"
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

func findShellExecutable() (string, error) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, "sh")
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		absolute, err := filepath.Abs(candidate)
		if err == nil {
			return absolute, nil
		}
	}
	return "", fmt.Errorf("could not find executable sh in PATH")
}

func getSessionManager() *SessionManager {
	sessionManagerMu.RLock()
	defer sessionManagerMu.RUnlock()
	return globalSessionManager
}

type ExecTool struct {
	workingDir           string
	timeout              time.Duration
	foregroundCap        time.Duration
	denyPatterns         []*regexp.Regexp
	hardlineDenyPatterns []*regexp.Regexp
	customDenyPatterns   []*regexp.Regexp
	customAllowPatterns  []*regexp.Regexp
	customUnlockPatterns []*regexp.Regexp
	// allowedPathPatterns are paths exec may read or modify. exec can mutate
	// any path it receives, so callers must pass write grants, not a read-only
	// allowlist.
	allowedPathPatterns  []*regexp.Regexp
	restrictToWorkspace  bool
	allowRemote          bool
	maxOutputChars       int
	maxInlineResultChars int
	baseEnv              map[string]string
	baseEnvList          []string
	state                *execStateStore
	sessionManager       *SessionManager
	riskGuard            *shellguard.Guard
	approvalEnabled      bool
	approvalReviewer     approvalReviewer
	blockBreaker         *execBlockBreaker
}

var _ AsyncExecutor = (*ExecTool)(nil)

var (
	defaultDenyPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\bdel\s+/[fq]\b`),
		regexp.MustCompile(`\brmdir\s+/s\b`),
		// Match disk wiping commands (must be followed by space/args)
		regexp.MustCompile(
			`(^|[^-\w])\b(format|mkfs|diskpart)\b\s`,
		),
		regexp.MustCompile(`\bdd\s+if=`),
		// Block writes to block devices (all common naming schemes).
		regexp.MustCompile(
			`>\s*/dev/(sd[a-z]|hd[a-z]|vd[a-z]|xvd[a-z]|nvme\d|mmcblk\d|loop\d|dm-\d|md\d|sr\d|nbd\d)`,
		),
		regexp.MustCompile(`\b(shutdown|reboot|poweroff)\b`),
		regexp.MustCompile(`:\(\)\s*\{.*\};\s*:`),
		regexp.MustCompile(`\$\([^)]+\)`),
		regexp.MustCompile(`\$\{[^}]+\}`),
		regexp.MustCompile("`[^`]+`"),
		regexp.MustCompile(`\|\s*sh\b`),
		regexp.MustCompile(`\|\s*bash\b`),
		regexp.MustCompile(`<<\s*EOF`),
		regexp.MustCompile(`\$\(\s*cat\s+`),
		regexp.MustCompile(`\$\(\s*curl\s+`),
		regexp.MustCompile(`\$\(\s*wget\s+`),
		regexp.MustCompile(`\$\(\s*which\s+`),
		regexp.MustCompile(`\bsudo\b`),
		regexp.MustCompile(`\bchmod\s+[0-7]{3,4}\b`),
		regexp.MustCompile(`\bchown\b`),
		regexp.MustCompile(`\bpkill\b`),
		regexp.MustCompile(`\bkillall\b`),
		regexp.MustCompile(`\bkill\b`),
		regexp.MustCompile(`\bcurl\b.*\|\s*(sh|bash)`),
		regexp.MustCompile(`\bwget\b.*\|\s*(sh|bash)`),
		regexp.MustCompile(`\bnpm\s+install\s+-g\b`),
		regexp.MustCompile(`\bpip\s+install\s+--user\b`),
		regexp.MustCompile(`\bapt\s+(install|remove|purge)\b`),
		regexp.MustCompile(`\byum\s+(install|remove)\b`),
		regexp.MustCompile(`\bdnf\s+(install|remove)\b`),
		regexp.MustCompile(`\bdocker\s+run\b`),
		regexp.MustCompile(`\bdocker\s+exec\b`),
		regexp.MustCompile(`\bgit\s+push\b`),
		regexp.MustCompile(`\bgit\s+force\b`),
		regexp.MustCompile(`\bssh\b.*@`),
		regexp.MustCompile(`\beval\b`),
		regexp.MustCompile(`\bsource\s+.*\.sh\b`),
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

	// windowsDenyPatterns contains PowerShell-specific deny patterns that only
	// apply on Windows, where commands are executed via powershell -Command.
	windowsDenyPatterns = []*regexp.Regexp{
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
		path = strings.TrimRight(path, string(filepath.Separator))
		if runtime.GOOS == "windows" {
			path = strings.TrimRight(path, "/")
		}
		if path == "" {
			continue
		}
		variants := []string{path}
		if runtime.GOOS == "windows" {
			variants = append(variants, filepath.ToSlash(path))
		}
		for _, variant := range variants {
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
	denyPatterns := make([]*regexp.Regexp, 0)
	customDenyPatterns := make([]*regexp.Regexp, 0)
	customAllowPatterns := make([]*regexp.Regexp, 0)
	customUnlockPatterns := make([]*regexp.Regexp, 0)
	var allowedPathPatterns []*regexp.Regexp
	// Fail closed when no config is supplied. Callers that need remote exec
	// must opt in explicitly through cfg.Tools.Exec.AllowRemote.
	allowRemote := false
	enableDefaultGuard := true
	approvalConfig := config.ApprovalsConfig{}
	if len(allowPaths) > 0 {
		allowedPathPatterns = allowPaths[0]
	}

	if cfg != nil {
		execConfig := cfg.Tools.Exec
		enableDefaultGuard = execConfig.EnableDenyPatterns
		allowRemote = execConfig.AllowRemote
		normalized, err := cfg.Approvals.Normalized()
		if err != nil {
			return nil, fmt.Errorf("approvals: %w", err)
		}
		approvalConfig = normalized
		if enableDefaultGuard {
			denyPatterns = append(denyPatterns, defaultDenyPatterns...)
			if runtime.GOOS == "windows" {
				denyPatterns = append(denyPatterns, windowsDenyPatterns...)
			}
		} else {
			// Only the optional built-in default set is disabled here. The
			// hardline floor and operator-defined custom deny rules still apply.
			logger.WarnCF(
				"tools",
				"default deny patterns are disabled; hardline and custom deny patterns still apply",
				nil,
			)
		}
		if len(execConfig.CustomDenyPatterns) > 0 {
			logger.InfoCF("tools", "using custom deny patterns", map[string]any{
				"patterns": execConfig.CustomDenyPatterns,
			})
			customDenyPatterns, err = compilePatterns(execConfig.CustomDenyPatterns, regexp.Compile, "custom deny")
			if err != nil {
				return nil, err
			}
		}
		customAllowPatterns, err = compilePatterns(
			execConfig.CustomAllowPatterns, compileFullMatchPattern, "custom allow",
		)
		if err != nil {
			return nil, err
		}
		customUnlockPatterns, err = compilePatterns(
			execConfig.CustomUnlockPatterns, compileFullMatchPattern, "custom unlock",
		)
		if err != nil {
			return nil, err
		}
	} else {
		denyPatterns = append(denyPatterns, defaultDenyPatterns...)
		if runtime.GOOS == "windows" {
			denyPatterns = append(denyPatterns, windowsDenyPatterns...)
		}
	}

	var timeout time.Duration
	if cfg != nil {
		timeout = time.Duration(cfg.Tools.Exec.GetTimeoutSeconds()) * time.Second
	} else {
		timeout = config.DefaultExecTimeoutSeconds * time.Second
	}

	maxOutputChars := config.DefaultExecMaxOutputChars
	maxInlineResultChars := 0
	var execEnvSet map[string]string
	var execEnvAllowlist []string
	if cfg != nil {
		maxOutputChars = cfg.Tools.Exec.GetMaxOutputChars()
		maxInlineResultChars = cfg.Tools.GetMaxInlineResultChars()
		execEnvSet = cfg.Tools.Exec.EnvSet
		execEnvAllowlist = cfg.Tools.Exec.EnvAllowlist
	}

	smartApprovals := approvalConfig.Mode == config.ApprovalModeSmart

	// Smart approvals need the AST classifier even when the optional default
	// guard set is disabled; otherwise unknown commands cannot be detected.
	riskGuard, err := newRiskGuard(cfg, enableDefaultGuard || smartApprovals)
	if err != nil {
		return nil, err
	}

	var reviewer approvalReviewer
	if smartApprovals {
		guardian, guardianErr := newApprovalGuardian(cfg, approvalConfig)
		if guardianErr != nil {
			// Keep safe commands working, but fail closed for every command
			// that needs a second opinion.
			logger.WarnCF(
				"tools.approval",
				"smart approvals enabled but the Guardian is unavailable; risky commands will be rejected",
				map[string]any{"error": guardianErr.Error()},
			)
		} else if guardian != nil {
			reviewer = guardian
		}
	}

	hardlinePatterns := make([]*regexp.Regexp, 0, len(hardlineDenyPatterns)+8)
	hardlinePatterns = append(hardlinePatterns, hardlineDenyPatterns...)
	hardlinePatterns = append(hardlinePatterns, selfProtectionHardlinePatterns()...)

	baseEnv := newExecEnv(workingDir, execEnvSet, execEnvAllowlist)
	return &ExecTool{
		workingDir:           workingDir,
		timeout:              timeout,
		foregroundCap:        maxForegroundTimeout,
		denyPatterns:         denyPatterns,
		hardlineDenyPatterns: hardlinePatterns,
		customDenyPatterns:   customDenyPatterns,
		customAllowPatterns:  customAllowPatterns,
		customUnlockPatterns: customUnlockPatterns,
		allowedPathPatterns:  allowedPathPatterns,
		restrictToWorkspace:  restrict,
		allowRemote:          allowRemote,
		maxOutputChars:       maxOutputChars,
		maxInlineResultChars: maxInlineResultChars,
		baseEnv:              baseEnv,
		baseEnvList:          buildEnvList(baseEnv),
		state:                newExecStateStore(),
		sessionManager:       getSessionManager(),
		riskGuard:            riskGuard,
		approvalEnabled:      smartApprovals,
		approvalReviewer:     reviewer,
		blockBreaker:         newExecBlockBreaker(),
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
	output := session.Tail(completionOutputChars)
	msg := fmt.Sprintf(
		"Background command completed.\nsessionId: %s\ncommand: %s\nstatus: %s\nexitCode: %d",
		session.ID, session.Command, status, session.GetExitCode(),
	)
	if output != "" {
		msg += "\n\nOutput tail:\n" + output
	}
	return NewToolResult(msg)
}

// executeRun handles action=run. A call passes through these stages in order;
// the first one that fails returns an error result and nothing is executed.
//
//  1. Arguments: command is required.
//  2. Remote gate: Execute rejects every action from a non-internal channel
//     unless allow_remote is set, so a run never reaches this function from
//     an untrusted caller.
//  3. Normalization: a trailing unquoted '&' becomes background=true so the
//     process is tracked. The guard still inspects the original command.
//  4. Timeout: must be a non-negative integer. A foreground request above the
//     foreground cap, or timeout=0, is promoted to a tracked background
//     session instead of being clamped or rejected.
//  5. Conversation state: only the remembered cwd for this agent/session is
//     loaded. Environment variables are not per-call state; the child always
//     starts from the sanitized base environment.
//  6. Working directory: explicit cwd, else the remembered cwd (revalidated),
//     else the workspace. With restrict_to_workspace an explicit cwd must
//     resolve inside the workspace.
//  7. Command guard: guardCommandContext runs the layered guard described in
//     its comment (hardline, patterns, workspace, risk and smart approvals).
//  8. Symlinks in cwd are resolved again right before execution to narrow the
//     TOCTOU window between validation and cmd.Dir.
//  9. State persistence: cwd is remembered only after the guard passed.
//  10. Launch: background and promoted runs go to runBackground, which returns
//     a sessionId; everything else goes to runSync. Both build the child env
//     from the sanitized base (allowlisted parent variables plus env_set) and
//     PICOCLAW_EXEC_*, and start the process through the isolation layer.
//     runSync bounds the output (head and tail, with the full stream spilled to
//     an artifact), kills the process tree on timeout and prefixes the result
//     with an "[exec] {...}" status header.
//
// Secret redaction is not done here. After the tool returns, the registry
// spills oversized results to an artifact that keeps the raw output on disk,
// and the agent loop then replaces known secrets in the model-facing content
// with [FILTERED] (tools.filter_sensitive_data).
func (t *ExecTool) executeRun(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	command, ok := args["command"].(string)
	if !ok {
		return ErrorResult("command is required")
	}

	isPty := boolArg(args, "pty")
	explicitBackground := boolArg(args, "background")
	isBackground := explicitBackground
	notify := boolArg(args, "notify")
	backgroundNote := ""

	// Keep the exact command for the safety guard. Normalization must not
	// change which deny or allow rules apply, for example a custom pattern that
	// matches a trailing '&'.
	guardedCommand := command

	// Parse once. The trailing-'&' converter, detach check, command guard and
	// exit note all consume the same AST instead of re-parsing the text.
	analysis := shellguard.Analyze(guardedCommand)

	// On Unix, `sh -c "server &"` returns as soon as the job starts: a
	// foreground call would report success while the process keeps running
	// untracked and its output pipe is closed. Normalize the trailing operator
	// into a tracked background session instead. Windows shells do not use '&'
	// as a background operator, so the helper is a no-op there.
	trailingConverted := false
	if normalized, converted := normalizeTrailingBackgroundAmpersand(command, analysis, runtime.GOOS); converted {
		trailingConverted = true
		command = normalized
		if !isBackground {
			isBackground = true
			backgroundNote = "Trailing '&' was converted to background=true so the process is tracked."
		}
	}

	timeout := t.timeout
	timeoutDisabled := false
	if value, provided, valid := numberArg(args, "timeout", 0); provided {
		const maxTimeoutSeconds = (1<<63 - 1) / int64(time.Second)
		if !valid || value > float64(maxTimeoutSeconds) {
			return ErrorResult("timeout must be a non-negative integer number of seconds")
		}
		seconds := int64(value)
		// timeout=0 means "no timeout", which is already how background sessions behave.
		if isBackground && seconds != 0 {
			if explicitBackground {
				return ErrorResult("timeout is only supported for foreground commands")
			}
			// The caller asked for a foreground timeout, but the trailing '&'
			// already moved the command to the background, where timeouts do
			// not apply. Keep the command and explain the ignored argument.
			backgroundNote += " The requested timeout was ignored because background sessions are not time-limited."
		} else {
			timeoutDisabled = seconds == 0
			timeout = time.Duration(seconds) * time.Second
		}
	}

	// The exec tool caps a foreground call at t.foregroundCap. Requests that
	// would exceed it (including timeout=0, which disables the limit) are
	// promoted to a tracked background session instead of being clamped or
	// rejected.
	promoteToBackground := !isBackground && t.foregroundCap > 0 &&
		(timeoutDisabled || timeout > t.foregroundCap)

	// A foreground nohup/setsid/disown or an inner '&' leaves a process the
	// session manager cannot track. The trailing '&' converter above already
	// turned the unambiguous single-operator form into a tracked run.
	if reason := untrackedDetachReason(
		command, analysis, isBackground || promoteToBackground, trailingConverted,
	); reason != "" {
		return ErrorResult(reason)
	}

	if isPty {
		if runtime.GOOS == "windows" {
			return ErrorResult("PTY is not supported on Windows. Use background=true without pty.")
		}
	}

	if notify && !isBackground && !promoteToBackground {
		return ErrorResult("notify is only supported with background=true or when the run is promoted to background")
	}

	savedCwd := t.state.load(ctx)

	requestedCwd, cwdProvided := args["cwd"].(string)
	cwdProvided = cwdProvided && requestedCwd != ""
	rememberedCwd := false

	cwd := t.workingDir
	switch {
	case cwdProvided:
		cwd = requestedCwd
	case savedCwd != "":
		if resolved, ok := t.rememberedCwd(ctx, savedCwd); ok {
			cwd = resolved
			rememberedCwd = true
		}
	}

	if cwdProvided && t.restrictToWorkspace && t.workingDir != "" {
		resolvedWD, err := t.validateCwd(cwd)
		if err != nil {
			return t.blockedResult(ctx, command, "Command blocked by safety guard ("+err.Error()+")")
		}
		cwd = resolvedWD
	}

	if cwd == "" {
		wd, err := os.Getwd()
		if err == nil {
			cwd = wd
		}
	}

	// Remember the path that passed workspace validation, not the post-symlink
	// resolved path: on platforms where the workspace itself lives behind a
	// symlink (for example /var -> /private/var on macOS), storing the resolved
	// path would fail validation on the next call.
	stateCwd := cwd

	if guardError := t.guardCommandWithAnalysis(ctx, guardedCommand, cwd, analysis); guardError != "" {
		return t.blockedResult(ctx, command, guardError)
	}

	// Re-resolve symlinks immediately before execution to shrink the TOCTOU window
	// between validation and cmd.Dir assignment.
	if t.restrictToWorkspace && t.workingDir != "" && cwd != t.workingDir {
		resolved, err := t.revalidateCwdForExecution(cwd)
		if err != nil {
			if rememberedCwd {
				t.forgetRememberedCwd(ctx, cwd, err)
			}
			return t.blockedResult(ctx, command, "Command blocked by safety guard ("+err.Error()+")")
		}
		cwd = resolved
	}

	// Persist only a cwd that passed validation and the command guard.
	if cwdProvided && stateCwd != "" {
		t.rememberCwd(ctx, stateCwd)
	}

	// Any allowed run breaks the blocked-command streak.
	t.blockBreaker.reset(ctx)

	if isBackground {
		return t.runBackground(ctx, command, cwd, isPty, backgroundNote, notify, cb)
	}
	if promoteToBackground {
		return t.runBackground(
			ctx,
			command,
			cwd,
			isPty,
			t.foregroundPromotionNote(timeoutDisabled, timeout),
			notify,
			cb,
		)
	}

	return t.runSync(ctx, command, cwd, timeout, analysis)
}

// resolvedWorkspace returns the workspace with symlinks resolved, so paths
// produced by `pwd -P` compare correctly on systems where the workspace itself
// lives behind a symlink (for example /var -> /private/var on macOS).
func (t *ExecTool) resolvedWorkspace() string {
	workspace := t.workingDir
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = resolved
	}
	return workspace
}

// cwdWithinPolicy reports whether a symlink-resolved directory is inside the
// resolved workspace or matches an explicit allowed path. Execution re-checks
// and the initial cwd validation share this predicate so they cannot drift.
func (t *ExecTool) cwdWithinPolicy(resolved string) bool {
	return fstools.IsAllowedPath(resolved, t.allowedPathPatterns) ||
		fstools.IsWithinWorkspace(resolved, t.resolvedWorkspace())
}

// revalidateCwdForExecution re-resolves symlinks right before the command
// starts, so a directory validated earlier cannot be swapped for one that
// escapes the workspace.
func (t *ExecTool) revalidateCwdForExecution(cwd string) (string, error) {
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", fmt.Errorf("path resolution failed: %w", err)
	}
	if !t.cwdWithinPolicy(resolved) {
		return "", errors.New("working directory escaped workspace")
	}
	return resolved, nil
}

// validateCwd applies the workspace and allow-path policy to a cwd. It tries
// the symlink-resolved form first so a workspace behind /var -> /private/var
// and a path reported by `pwd -P` compare correctly; the original pair is the
// fallback for operator allow patterns that name the unresolved path.
func (t *ExecTool) validateCwd(cwd string) (string, error) {
	if !t.restrictToWorkspace || t.workingDir == "" {
		return cwd, nil
	}
	candidate := cwd
	if resolved, err := filepath.EvalSymlinks(candidate); err == nil {
		candidate = resolved
	}
	if validated, err := fstools.ValidatePathWithAllowPaths(
		candidate, t.resolvedWorkspace(), true, t.allowedPathPatterns,
	); err == nil {
		return validated, nil
	}
	return fstools.ValidatePathWithAllowPaths(cwd, t.workingDir, true, t.allowedPathPatterns)
}

// acceptObservedCwd reports whether the cwd observed after a command is a real
// directory that the workspace policy allows. The observed path is used for
// the result header even when it is rejected, but only accepted paths are
// remembered for the next call.
func (t *ExecTool) acceptObservedCwd(cwd string) bool {
	if cwd == "" || !filepath.IsAbs(cwd) {
		return false
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = t.validateCwd(cwd)
	return err == nil
}

// rememberedCwd revalidates a cwd stored for the conversation. It returns
// ok=false and forgets the value when the directory disappeared or no longer
// satisfies the workspace policy, so the caller can fall back to the default.
func (t *ExecTool) rememberedCwd(ctx context.Context, cwd string) (string, bool) {
	if cwd == "" {
		return "", false
	}
	resolved := cwd
	if t.restrictToWorkspace && t.workingDir != "" {
		validated, err := t.validateCwd(cwd)
		if err != nil {
			t.forgetRememberedCwd(ctx, cwd, err)
			return "", false
		}
		resolved = validated
	}
	info, err := os.Stat(resolved)
	if err != nil {
		t.forgetRememberedCwd(ctx, cwd, err)
		return "", false
	}
	if !info.IsDir() {
		t.forgetRememberedCwd(ctx, cwd, fmt.Errorf("not a directory"))
		return "", false
	}
	if abs, err := filepath.Abs(resolved); err == nil {
		resolved = abs
	}
	return resolved, true
}

func (t *ExecTool) forgetRememberedCwd(ctx context.Context, cwd string, cause error) {
	if t.state != nil {
		t.state.clearCwd(ctx)
	}
	logger.WarnCF("shell", "forgetting unusable remembered cwd", map[string]any{
		"cwd":   cwd,
		"error": cause.Error(),
	})
}

func (t *ExecTool) rememberCwd(ctx context.Context, cwd string) {
	if t.state == nil {
		return
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	t.state.rememberCwd(ctx, cwd)
}

func (t *ExecTool) SetTimeout(timeout time.Duration) {
	t.timeout = timeout
}
