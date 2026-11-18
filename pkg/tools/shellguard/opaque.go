package shellguard

import "strings"

// interpreterOpaqueSpec describes the options that decide whether an
// interpreter program is visible in argv.
type interpreterOpaqueSpec struct {
	inline map[string]bool
	module map[string]bool
	value  map[string]bool
	file   map[string]bool
}

func flags(values ...string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

var pythonOpaqueSpec = interpreterOpaqueSpec{
	inline: flags("-c", "--command"),
	module: flags("-m", "--module"),
}

var perlOpaqueSpec = interpreterOpaqueSpec{
	inline: flags("-e", "-E", "--eval"),
	module: flags("-M", "-m"),
}

var nodeOpaqueSpec = interpreterOpaqueSpec{
	inline: flags("-e", "--eval", "-p", "--print"),
}

var rubyOpaqueSpec = interpreterOpaqueSpec{
	inline: flags("-e", "--eval"),
	value:  flags("-E", "--encoding", "-I", "--include", "-r", "--require"),
}

var phpOpaqueSpec = interpreterOpaqueSpec{
	inline: flags("-r", "--run"),
	file:   flags("-f", "--file"),
}

var luaOpaqueSpec = interpreterOpaqueSpec{
	inline: flags("-e", "--execute"),
	value:  flags("-l", "--load"),
}

var rscriptOpaqueSpec = interpreterOpaqueSpec{
	inline: flags("-e", "--expr"),
}

var osascriptOpaqueSpec = interpreterOpaqueSpec{
	inline: flags("-e", "--eval"),
}

// opaqueInterpreterSpec maps the interpreter names in interpreterFlags to the
// options that make their program visible or opaque. A nil spec uses the
// generic rule for a command that only takes an inline -e/-c program.
var opaqueInterpreterSpec = map[string]interpreterOpaqueSpec{
	"python":    pythonOpaqueSpec,
	"python2":   pythonOpaqueSpec,
	"python3":   pythonOpaqueSpec,
	"pypy":      pythonOpaqueSpec,
	"pypy3":     pythonOpaqueSpec,
	"perl":      perlOpaqueSpec,
	"perl5":     perlOpaqueSpec,
	"node":      nodeOpaqueSpec,
	"nodejs":    nodeOpaqueSpec,
	"ruby":      rubyOpaqueSpec,
	"php":       phpOpaqueSpec,
	"lua":       luaOpaqueSpec,
	"luajit":    luaOpaqueSpec,
	"rscript":   rscriptOpaqueSpec,
	"osascript": osascriptOpaqueSpec,
}

// opaqueInterpreterReason reports an interpreter invocation whose program is
// not visible in argv: a script file, stdin, or a module loader. The risk
// classifier treats such commands as unknown so the strict policy reviews and
// blocks them instead of letting the interpreter execute arbitrary code.
func opaqueInterpreterReason(words []word) (string, bool) {
	if len(words) == 0 || words[0].dynamic {
		return "", false
	}
	name := strings.ToLower(baseCommand(words[0].text))
	if isShellCommand(name) {
		return "", false
	}
	flag, known := interpreterFlags[name]
	if !known {
		return "", false
	}

	args := words[1:]
	if len(args) == 0 {
		return name + " reads commands from stdin, which cannot be inspected", true
	}
	// awk and sed take the program as the first non-option argument unless
	// -f/--file names a script file. Their inline programs are already handled
	// by CheckInterpreterFile, so only the file form is opaque.
	if flag == "" || name == "sed" || name == "gsed" {
		for _, arg := range args {
			if arg.dynamic || arg.text == "" {
				continue
			}
			if isScriptFileFlag(arg.text) {
				return name + " reads a script file that cannot be inspected", true
			}
		}
		return "", false
	}

	spec, ok := opaqueInterpreterSpec[name]
	if !ok {
		// A known inline interpreter without a specific spec: treat an inline
		// flag as visible and every other non-option as a script file.
		spec = interpreterOpaqueSpec{inline: flags(flag)}
	}

	infoOnly := true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg.dynamic {
			return name + " has a dynamic argument that cannot be inspected", true
		}
		text := arg.text
		if text == "" {
			continue
		}
		if text == "--" {
			if i+1 < len(args) {
				return name + " reads a script file that cannot be inspected", true
			}
			return name + " reads stdin, which cannot be inspected", true
		}
		if text == "-" {
			return name + " reads stdin, which cannot be inspected", true
		}
		if spec.inline[text] {
			if i+1 >= len(args) || args[i+1].dynamic {
				return name + " has a dynamic inline program that cannot be inspected", true
			}
			return "", false
		}
		if spec.module[text] {
			return name + " loads a module that cannot be inspected", true
		}
		if spec.file[text] || isScriptFileFlag(text) {
			return name + " reads a script file that cannot be inspected", true
		}
		if strings.HasPrefix(text, "-") {
			if spec.value[text] {
				i++
				infoOnly = false
				continue
			}
			if !isInterpreterInfoFlag(text) {
				infoOnly = false
			}
			continue
		}
		return name + " reads a script file that cannot be inspected: " + text, true
	}
	if infoOnly {
		return "", false
	}
	return name + " reads commands from stdin, which cannot be inspected", true
}

func isScriptFileFlag(text string) bool {
	return text == "-f" || text == "--file" ||
		strings.HasPrefix(text, "--file=") ||
		(strings.HasPrefix(text, "-f") && len(text) > 2)
}

func isInterpreterInfoFlag(flag string) bool {
	switch strings.ToLower(flag) {
	case "-h", "--help", "-v", "--version", "-version", "--copyright":
		return true
	default:
		return false
	}
}
