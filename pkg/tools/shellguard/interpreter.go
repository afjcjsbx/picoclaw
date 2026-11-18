package shellguard

import (
	"path/filepath"
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// InterpreterPolicy extends the workspace boundary to code passed inline to an
// interpreter. A shell command such as
//
//	python3 -c "shutil.rmtree('/tmp/x')"
//
// contains no shell path operand, so the normal workspace pass cannot see the
// path inside the quoted program. The interpreter pass parses the argv, finds
// inline code, and rejects it when the code performs a filesystem or process
// operation and mentions a path outside the workspace.
//
// This is a static, best-effort check. It deliberately does not try to execute
// or symbolically evaluate the program; the OS isolation layer remains the
// hard boundary.

// interpreterFlags maps a command name to the option that introduces inline
// code. An empty value means every argument may contain code (awk and sed).
var interpreterFlags = map[string]string{
	"python":    "-c",
	"python2":   "-c",
	"python3":   "-c",
	"pypy":      "-c",
	"pypy3":     "-c",
	"perl":      "-e",
	"perl5":     "-e",
	"node":      "-e",
	"nodejs":    "-e",
	"ruby":      "-e",
	"php":       "-r",
	"lua":       "-e",
	"luajit":    "-e",
	"rscript":   "-e",
	"awk":       "",
	"gawk":      "",
	"mawk":      "",
	"nawk":      "",
	"sed":       "-e",
	"gsed":      "-e",
	"osascript": "-e",
	"sh":        "-c",
	"bash":      "-c",
	"zsh":       "-c",
	"dash":      "-c",
	"ksh":       "-c",
}

// interpreterOperationMarkers are the filesystem and process operations whose
// presence turns a mentioned path into an actual access. Pure computation such
// as `python3 -c "print(1+1)"` contains none of them and stays allowed.
var interpreterOperationMarkers = []string{
	"rmtree", "remove_tree", "unlink", "rmdir", "rm_sync", "rmsync",
	"os.remove", "os.rename", "os.replace", "shutil.move", "fileutils.rm",
	"writefile", "writefilesync", "appendfile", "createwritestream",
	"write.csv", "write.table", "saverds", "file.write", "file.delete",
	"open(", "fopen(", "io.open(", "path(", "files.", "fileutils.",
	"system(", "popen", "subprocess", "os.system", "os.exec", "osexec",
	"child_process", "execsync", "execfile", "spawn(", "eval(",
	"do shell script", "do javascript",
	" rm ", "rm -", "rmdir ", "unlink ", "mv ", "cp ", "chmod ", "chown ",
	"tee ", "truncate ", "dd if=", ">",
}

// interpreterPathPattern matches a path-like word inside inline code. The
// prefix avoids URLs: the slash in `https://` is preceded by a colon, which is
// not in the prefix class.
var interpreterPathPattern = regexp.MustCompile(
	`(?:^|[\s'"=(,:;\[])((?:/|\.\./|~/|\$HOME(?:/|["'])|~)[^\s'"` + "`" + `;,)\]}>]*)`,
)

// CheckInterpreterFile rejects inline interpreter code that combines a
// filesystem/process operation with a path outside the workspace.
func CheckInterpreterFile(command string, file *syntax.File, policy WorkspacePolicy, depth int) error {
	if file == nil || depth > maxShellNesting {
		return nil
	}
	var violation error
	sawInterpreter := false
	syntax.Walk(file, func(node syntax.Node) bool {
		if violation != nil {
			return false
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		words := make([]word, 0, len(call.Args))
		for _, arg := range call.Args {
			words = append(words, literalWord(arg))
		}
		if words[0].dynamic || words[0].text == "" {
			return true
		}
		name := strings.ToLower(baseCommand(words[0].text))
		flag, known := interpreterFlags[name]
		if !known {
			return true
		}
		sawInterpreter = true

		payloads := interpreterPayloads(words[1:], flag)
		if len(payloads) == 0 {
			return true
		}
		// Scan every argument, not only the inline program: a program that
		// reads sys.argv[1] is still dangerous when the path is supplied as a
		// separate argv word.
		allText := make([]string, 0, len(words))
		for _, w := range words[1:] {
			if !w.dynamic && w.text != "" {
				allText = append(allText, w.text)
			}
		}
		if !interpreterHasOperation(payloads) {
			return true
		}
		for _, text := range allText {
			if path, outside := interpreterOutsidePath(text, policy); outside {
				violation = newWorkspaceError(
					"interpreter inline code performs a filesystem or process operation on " +
						path + ", which is outside the workspace",
				)
				return false
			}
		}
		return true
	})
	if violation == nil && sawInterpreter && interpreterDynamicHomeOperation(command) {
		violation = newWorkspaceError(
			"interpreter inline code performs a filesystem or process operation on a " +
				"home-relative path, which is outside the workspace",
		)
	}
	return violation
}

// interpreterDynamicHomeOperation covers $HOME and ~ inside a double-quoted
// program: the shell expands them before the interpreter sees the code, so the
// parsed word is dynamic and the literal payload check cannot resolve it. A
// coarse scan over the raw command is safe here because it only runs after an
// interpreter call was found.
func interpreterDynamicHomeOperation(command string) bool {
	if !strings.Contains(command, "$HOME") && !strings.Contains(command, "~") {
		return false
	}
	lower := strings.ToLower(command)
	for _, marker := range interpreterOperationMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// interpreterPayloads returns the inline code arguments for one interpreter.
func interpreterPayloads(args []word, flag string) []string {
	if flag == "" {
		// awk/sed: the program is normally the first non-option argument, but
		// scanning all of them is safe and catches -e/-f forms too.
		out := make([]string, 0, len(args))
		for _, arg := range args {
			if arg.dynamic || arg.text == "" || strings.HasPrefix(arg.text, "-") {
				continue
			}
			out = append(out, arg.text)
		}
		return out
	}
	for i, arg := range args {
		if arg.dynamic || arg.text == "" {
			continue
		}
		if arg.text == flag {
			if i+1 < len(args) && !args[i+1].dynamic {
				return []string{args[i+1].text}
			}
			return nil
		}
		// Support the attached form, for example python3 -c'print(1)'.
		if strings.HasPrefix(arg.text, flag) && len(arg.text) > len(flag) {
			return []string{arg.text[len(flag):]}
		}
	}
	return nil
}

func interpreterHasOperation(payloads []string) bool {
	for _, payload := range payloads {
		lower := strings.ToLower(payload)
		for _, marker := range interpreterOperationMarkers {
			if strings.Contains(lower, marker) {
				return true
			}
		}
	}
	return false
}

// interpreterOutsidePath reports whether text mentions a path outside the
// workspace. Pseudo-devices and the workspace itself are allowed.
func interpreterOutsidePath(text string, policy WorkspacePolicy) (string, bool) {
	for _, match := range interpreterPathPattern.FindAllStringSubmatch(text, -1) {
		raw := match[1]
		path, ok := interpreterExpandPath(raw, policy.Home, policy.Cwd)
		if !ok {
			continue
		}
		if isSafeDevicePath(path) {
			continue
		}
		if policy.AllowPath != nil {
			if policy.AllowPath(path) {
				continue
			}
		} else if workspacePathWithin(policy.Cwd, path) {
			continue
		}
		return path, true
	}
	return "", false
}

func interpreterExpandPath(raw string, home string, cwd string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	raw = strings.TrimSuffix(raw, ".")
	raw = strings.ReplaceAll(raw, `\/`, "/")
	raw = strings.ReplaceAll(raw, `\\`, `\`)
	switch {
	case raw == "~" || strings.HasPrefix(raw, "~/"):
		if home == "" {
			return raw, true // unresolvable home: treat as outside
		}
		raw = home + strings.TrimPrefix(raw, "~")
	case strings.HasPrefix(raw, "$HOME/") || raw == "$HOME":
		if home == "" {
			return raw, true
		}
		raw = home + strings.TrimPrefix(raw, "$HOME")
	}
	if !filepath.IsAbs(raw) {
		if cwd == "" {
			return raw, true // unresolvable relative path: treat as outside
		}
		raw = filepath.Join(cwd, raw)
	}
	return filepath.Clean(raw), true
}

// workspacePathWithin reports whether child is parent or below parent.
func workspacePathWithin(parent, child string) bool {
	if parent == "" || child == "" {
		return false
	}
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isSafeDevicePath(path string) bool {
	switch path {
	case "/dev/null", "/dev/stdin", "/dev/stdout", "/dev/stderr", "/dev/tty":
		return true
	}
	return strings.HasPrefix(path, "/dev/fd/")
}
