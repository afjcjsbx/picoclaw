//go:build !windows

package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	fstools "github.com/sipeed/picoclaw/pkg/tools/fs"
	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

// Platform behavior of the exec tool. The posix and windows files define the
// same set of names, so the rest of the package does not branch on the OS.
const (
	// posixShell reports that commands run through sh, so the POSIX parse
	// describes what runs and the AST layers apply.
	posixShell = true
	// ptySupported reports whether commands can run in a pseudo-terminal.
	ptySupported = true
)

// platformDenyPatterns are the text deny patterns of the default guard. Unix
// has none: its constructs are checked on the parse (see matchesDefaultDeny).
func platformDenyPatterns() []*regexp.Regexp { return nil }

// shellCommand returns the OS shell and its arguments for one exec run.
func shellCommand(command string) (string, []string, error) {
	shellPath, err := findShellExecutable()
	if err != nil {
		return "", nil, fmt.Errorf("failed to locate shell: %w", err)
	}
	return shellPath, []string{"-c", command}, nil
}

// shellQuote single-quotes a path for the POSIX shell.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// wrapCommandForCwd appends a probe that writes `pwd -P` after the command and
// preserves its exit status. The wrapper runs in the same shell, so `cd` inside
// the command is observed. A command that cannot safely take a suffix (a
// trailing backslash continuation) is left unchanged.
func wrapCommandForCwd(command, probePath string) (string, bool) {
	if probePath == "" {
		return command, false
	}
	if strings.HasSuffix(strings.TrimRight(command, " \t\r\n"), "\\") {
		return command, false
	}
	wrapped := command + "\n__picoclaw_exec_status=$?\n" +
		"command pwd -P > " + shellQuote(probePath) + " 2>/dev/null\n" +
		"exit $__picoclaw_exec_status"
	return wrapped, true
}

// denyFormsOf lists the text forms the deny patterns match. Quoted heredoc
// bodies are literal text: a backtick there cannot run a command, so deny
// patterns must not inspect it.
func denyFormsOf(cmd string, analysis shellguard.Analysis) []string {
	// The caller parsed the untrimmed command, so trim the stripped view.
	stripped := strings.TrimSpace(analysis.StrippedHeredoc)

	// POSIX shells can hide a command behind fixed obfuscation such as $IFS,
	// ANSI-C quoting or a backslash-newline. Resolve those forms and let the AST
	// expose quote-spliced argv words. This runs even when the optional risk
	// classifier is nil, so the hardline floor stays independent of
	// enable_deny_patterns.
	canonicalForms := analysis.Forms(cmd)
	forms := make([]string, 0, 1+len(canonicalForms))
	forms = append(forms, strings.ToLower(shellguard.NormalizeForDeny(stripped)))
	// Canonicalize every command, not just ones with quoting: wrappers such as
	// env and command expose the nested argv without any special shell syntax,
	// and custom deny patterns must see it.
	for _, form := range canonicalForms {
		forms = append(forms, strings.ToLower(form))
	}
	return forms
}

// checkWorkspacePaths applies the AST workspace guard to the command.
func (t *ExecTool) checkWorkspacePaths(in *guardInput, cwdPath string) error {
	policy := shellguard.WorkspacePolicy{
		Cwd:    cwdPath,
		Home:   t.execHomeDir(),
		CDPath: t.baseEnv["CDPATH"] != "",
		AllowPath: func(p string) bool {
			return t.workspacePathAllowed(p, cwdPath)
		},
	}
	if err := shellguard.CheckWorkspaceFile(in.command, in.analysis.File, policy); err != nil {
		return blockedBySafetyGuard(err.Error())
	}
	return nil
}

// workspacePathAllowed reports whether p stays inside cwdPath or is covered by
// the operator allowlist. Symlinks are resolved against the nearest existing
// ancestor so a new file behind a symlinked directory is still checked.
func (t *ExecTool) workspacePathAllowed(p, cwdPath string) bool {
	if resolved, err := fstools.ResolvePathAgainstExistingAncestor(p); err == nil {
		p = resolved
	}
	if safePaths[p] {
		return true
	}
	if fstools.IsAllowedPath(p, t.allowedPathPatterns) {
		return true
	}
	return fstools.IsWithinWorkspace(p, cwdPath)
}

// execHomeDir returns the home directory the child shell will see. A HOME
// present in the cached base environment (config env_set or inherited) wins,
// even when it is not absolute; tilde expansion is then unresolvable and
// blocked. os.UserHomeDir is only a fallback when the key is absent.
func (t *ExecTool) execHomeDir() string {
	if home, ok := t.baseEnv["HOME"]; ok {
		if filepath.IsAbs(home) {
			return home
		}
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		return home
	}
	return ""
}

// execEnvKey normalizes a variable name.
func execEnvKey(key string) string { return key }

// platformEnvAllowlist lists the variables inherited on top of the default
// allowlist.
func platformEnvAllowlist() map[string]bool { return nil }

// protectedPathForms lists the spellings of a protected path that a delete
// command could use.
func protectedPathForms(path string) []string {
	path = strings.TrimRight(path, string(filepath.Separator))
	if path == "" {
		return nil
	}
	return []string{path}
}
