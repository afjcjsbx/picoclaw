package tools

import (
	"path/filepath"
	"strings"
)

// The workspace guard must tell apart paths that are executed from paths that
// are read or written. A relative executable such as .venv/bin/python may be a
// symlink whose target lives outside the workspace; that is how venv, nvm,
// pyenv and rustup install their shims. Executing such a path is equivalent to
// running a bare command resolved through PATH, which the guard already allows.
// The symlink resolution check is still required for every non-executable
// operand: that is what prevents reads and writes escaping the workspace.

// shellWord is a word in command position, with byte offsets into the command.
type shellWord struct {
	text       string
	start, end int
}

type shellTokenKind uint8

const (
	shellTokenWord shellTokenKind = iota
	shellTokenOperator
	shellTokenRedirect
)

type shellToken struct {
	kind       shellTokenKind
	text       string
	start, end int
}

// shellExecutableWords returns the words that the shell may execute. The
// second result is false when the command uses syntax this lightweight parser
// does not model; callers must then fall back to resolving every symlink.
func shellExecutableWords(command string) ([]shellWord, bool) {
	tokens, ok := lexShellTokens(command)
	if !ok {
		return nil, false
	}
	return classifyShellTokens(tokens)
}

func isExecutableShellWord(words []shellWord, pos int) bool {
	for _, word := range words {
		if pos >= word.start && pos < word.end {
			return true
		}
	}
	return false
}

func isPathWithin(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	return err == nil && filepath.IsLocal(rel)
}

func lexShellTokens(command string) ([]shellToken, bool) {
	tokens := make([]shellToken, 0, 8)
	for i := 0; i < len(command); {
		switch command[i] {
		case ' ', '\t', '\r':
			i++
			continue
		case '\n':
			tokens = append(tokens, shellToken{
				kind: shellTokenOperator, text: "\n", start: i, end: i + 1,
			})
			i++
			continue
		case '#':
			for i < len(command) && command[i] != '\n' {
				i++
			}
			continue
		case '\\':
			if i+1 < len(command) && command[i+1] == '\n' {
				i += 2
				continue
			}
		}

		if opLen, unsupported, ok := shellRedirectAt(command, i); ok {
			if unsupported {
				return nil, false
			}
			tokens = append(tokens, shellToken{
				kind: shellTokenRedirect, text: command[i : i+opLen], start: i, end: i + opLen,
			})
			i += opLen
			continue
		}

		if op := shellOperatorAt(command, i); op != "" {
			tokens = append(tokens, shellToken{
				kind: shellTokenOperator, text: op, start: i, end: i + len(op),
			})
			i += len(op)
			continue
		}

		end, ok := scanShellWord(command, i)
		if !ok || end <= i {
			return nil, false
		}
		tokens = append(tokens, shellToken{
			kind: shellTokenWord, text: command[i:end], start: i, end: end,
		})
		i = end
	}
	return tokens, true
}

var shellOperatorTokens = []string{
	"&>>", ";;&",
	"&&", "&>", "||", "|&", ";;", ";&",
	";", "|", "&", "(", ")",
}

func shellOperatorAt(command string, start int) string {
	for _, op := range shellOperatorTokens {
		if strings.HasPrefix(command[start:], op) {
			return op
		}
	}
	return ""
}

// shellRedirectAt recognizes redirections, including their optional file
// descriptor prefix. Heredocs and process substitutions are reported as
// unsupported because their bodies are not shell words in the usual sense.
func shellRedirectAt(command string, start int) (opLen int, unsupported, ok bool) {
	i := start
	for i < len(command) && command[i] >= '0' && command[i] <= '9' {
		i++
	}

	if i == start && i+1 < len(command) && command[i] == '&' && command[i+1] == '>' {
		switch {
		case strings.HasPrefix(command[i:], "&>>"):
			return 3, false, true
		case strings.HasPrefix(command[i:], "&>"):
			return 2, false, true
		}
	}

	if i >= len(command) || (command[i] != '<' && command[i] != '>') {
		return 0, false, false
	}

	rest := command[i:]
	switch {
	case strings.HasPrefix(rest, "<<<"):
		return i - start + 3, false, true
	case strings.HasPrefix(rest, "<<"):
		return 0, true, true
	case strings.HasPrefix(rest, ">>"),
		strings.HasPrefix(rest, ">&"),
		strings.HasPrefix(rest, "<&"),
		strings.HasPrefix(rest, ">|"),
		strings.HasPrefix(rest, "<>"):
		return i - start + 2, false, true
	case strings.HasPrefix(rest, ">"), strings.HasPrefix(rest, "<"):
		if i+1 < len(command) && command[i+1] == '(' {
			return 0, true, true
		}
		return i - start + 1, false, true
	default:
		return 0, false, false
	}
}

func scanShellWord(command string, start int) (int, bool) {
	for i := start; i < len(command); {
		switch command[i] {
		case ' ', '\t', '\r', '\n', ';', '|', '&', '<', '>':
			return i, true
		case '(', ')':
			// Parentheses inside a word are extglob or another construct this
			// parser does not model. Fall back to resolving every symlink.
			return 0, false
		case '\'':
			j := i + 1
			for j < len(command) && command[j] != '\'' {
				j++
			}
			if j >= len(command) {
				return 0, false
			}
			i = j + 1
		case '"':
			j := i + 1
			closed := false
			for j < len(command) {
				if command[j] == '\\' {
					j += 2
					continue
				}
				if command[j] == '"' {
					j++
					closed = true
					break
				}
				j++
			}
			if !closed {
				return 0, false
			}
			i = j
		case '\\':
			if i+1 >= len(command) {
				return 0, false
			}
			i += 2
		case '`':
			return 0, false
		case '$':
			if i+1 < len(command) && command[i+1] == '(' {
				return 0, false
			}
			i++
		default:
			i++
		}
	}
	return len(command), true
}

func classifyShellTokens(tokens []shellToken) ([]shellWord, bool) {
	executable := make([]shellWord, 0, 4)
	expectCommand := true
	skipRedirectTarget := false

	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch token.kind {
		case shellTokenOperator:
			expectCommand = true
			skipRedirectTarget = false
			continue
		case shellTokenRedirect:
			skipRedirectTarget = true
			continue
		}

		if skipRedirectTarget {
			skipRedirectTarget = false
			continue
		}
		if !expectCommand {
			continue
		}

		name := shellWordText(token.text)
		switch {
		case isShellAssignment(token.text):
			continue
		case isShellCommandPrefix(name):
			continue
		case isUnsupportedShellKeyword(name):
			return nil, false
		}

		executable = append(executable, shellWord{
			text: token.text, start: token.start, end: token.end,
		})
		expectCommand = false

		if spec, ok := shellWrapperSpecs[name]; ok {
			nested, ok := wrapperCommandWords(tokens, i+1, spec)
			if !ok {
				return nil, false
			}
			executable = append(executable, nested...)
		}
	}

	return executable, true
}

func shellWordText(word string) string {
	for len(word) >= 2 {
		if (word[0] == '\'' && word[len(word)-1] == '\'') ||
			(word[0] == '"' && word[len(word)-1] == '"') {
			word = word[1 : len(word)-1]
			continue
		}
		break
	}
	return word
}

func isShellAssignment(word string) bool {
	eq := strings.IndexByte(word, '=')
	if eq <= 0 {
		return false
	}
	name := strings.TrimSuffix(word[:eq], "+")
	if name == "" {
		return false
	}
	if bracket := strings.IndexByte(name, '['); bracket >= 0 {
		if !strings.HasSuffix(name, "]") {
			return false
		}
		name = name[:bracket]
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if i == 0 {
			if !isShellNameStart(c) {
				return false
			}
			continue
		}
		if !isShellNameChar(c) {
			return false
		}
	}
	return true
}

func isShellNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isShellNameChar(c byte) bool {
	return isShellNameStart(c) || (c >= '0' && c <= '9')
}

func isShellCommandPrefix(word string) bool {
	switch word {
	case "if", "then", "elif", "else", "while", "until", "do",
		"!", "{", "}", "fi", "done", "esac":
		return true
	}
	return false
}

func isUnsupportedShellKeyword(word string) bool {
	switch word {
	case "for", "case", "select", "function", "coproc":
		return true
	}
	return false
}

type shellWrapperSpec struct {
	flags        map[string]bool
	valueOptions map[string]bool
	positionals  int
	assignments  bool
}

var shellWrapperSpecs = map[string]shellWrapperSpec{
	"env": {
		flags: shellOptionSet(
			"-i", "-0", "-v", "--ignore-environment", "--null", "--debug",
			"--list-signal-handling", "--help", "--version",
		),
		valueOptions: shellOptionSet(
			"-u", "--unset", "-C", "--chdir", "-S", "--split-string",
			"--block-signal", "--default-signal", "--ignore-signal",
		),
		assignments: true,
	},
	"command": {
		flags: shellOptionSet("-p", "--help"),
	},
	"exec": {
		flags:        shellOptionSet("-c", "-l"),
		valueOptions: shellOptionSet("-a"),
	},
	"nohup": {
		flags: shellOptionSet("--help", "--version"),
	},
	"nice": {
		flags:        shellOptionSet("--help", "--version"),
		valueOptions: shellOptionSet("-n", "--adjustment"),
	},
	"timeout": {
		flags: shellOptionSet(
			"--preserve-status", "--foreground", "-v", "--verbose", "--help", "--version",
		),
		valueOptions: shellOptionSet("-s", "--signal", "-k", "--kill-after"),
		positionals:  1, // duration
	},
	"stdbuf": {
		flags: shellOptionSet("--help", "--version"),
		valueOptions: shellOptionSet(
			"-i", "-o", "-e", "--input", "--output", "--error",
		),
	},
	"setsid": {
		flags: shellOptionSet(
			"-c", "--ctty", "-f", "--fork", "-w", "--wait", "--help", "--version",
		),
	},
	"time": {
		flags: shellOptionSet("-p"),
	},
	"sudo": {
		flags: shellOptionSet(
			"-A", "-b", "-E", "-H", "-n", "-P", "-S",
			"--askpass", "--background", "--bell", "--preserve-env",
			"--set-home", "--non-interactive", "--preserve-groups", "--stdin",
			"--help", "--version",
		),
		valueOptions: shellOptionSet(
			"-C", "--close-from", "-D", "--chdir", "-g", "--group",
			"-h", "--host", "-p", "--prompt", "-R", "--chroot", "-r", "--role",
			"-T", "--command-timeout", "-t", "--type", "-U", "--other-user",
			"-u", "--user",
		),
	},
	"chroot": {
		flags:        shellOptionSet("--help", "--version"),
		valueOptions: shellOptionSet("--userspec", "--groups"),
		positionals:  1, // new root
	},
	"runuser": {
		flags: shellOptionSet(
			"-l", "--login", "-m", "-p", "--preserve-environment",
		),
		valueOptions: shellOptionSet(
			"-u", "-g", "-G", "-c", "-s", "-w",
			"--session-command", "--command", "--shell",
		),
	},
}

func shellOptionSet(options ...string) map[string]bool {
	set := make(map[string]bool, len(options))
	for _, option := range options {
		set[option] = true
	}
	return set
}

func wrapperCommandWords(tokens []shellToken, start int, spec shellWrapperSpec) ([]shellWord, bool) {
	index, ok := wrapperCommandIndex(tokens, start, spec)
	if !ok || index < 0 {
		return nil, ok
	}
	token := tokens[index]
	words := []shellWord{{text: token.text, start: token.start, end: token.end}}
	if nested, ok := shellWrapperSpecs[shellWordText(token.text)]; ok {
		more, ok := wrapperCommandWords(tokens, index+1, nested)
		if !ok {
			return nil, false
		}
		words = append(words, more...)
	}
	return words, true
}

func wrapperCommandIndex(tokens []shellToken, start int, spec shellWrapperSpec) (int, bool) {
	positionals := spec.positionals
	endOptions := false

	for i := start; i < len(tokens); i++ {
		token := tokens[i]
		switch token.kind {
		case shellTokenOperator:
			return -1, true
		case shellTokenRedirect:
			if i+1 < len(tokens) {
				i++
			}
			continue
		}

		if !endOptions && token.text == "--" {
			endOptions = true
			continue
		}
		if !endOptions && spec.assignments && isShellAssignment(token.text) {
			continue
		}
		if !endOptions && strings.HasPrefix(token.text, "-") && token.text != "-" {
			takesValue, known := wrapperOptionValue(token.text, spec)
			if !known {
				return -1, false
			}
			if takesValue {
				if i+1 >= len(tokens) || tokens[i+1].kind != shellTokenWord {
					return -1, false
				}
				i++
			}
			continue
		}
		if positionals > 0 {
			positionals--
			continue
		}
		return i, true
	}
	return -1, true
}

func wrapperOptionValue(option string, spec shellWrapperSpec) (takesValue, known bool) {
	if strings.HasPrefix(option, "--") {
		name := option
		if eq := strings.IndexByte(option, '='); eq >= 0 {
			name = option[:eq]
			return false, spec.flags[name] || spec.valueOptions[name]
		}
		return spec.valueOptions[name], spec.flags[name] || spec.valueOptions[name]
	}

	for i := 1; i < len(option); i++ {
		name := "-" + string(option[i])
		if spec.valueOptions[name] {
			return i == len(option)-1, true
		}
		if !spec.flags[name] {
			return false, false
		}
	}
	return false, true
}
