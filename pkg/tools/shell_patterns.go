package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
)

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
