package shellguard

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// maxShellNesting bounds recursive analysis of sh -c scripts and wrapper
// commands. Real commands nest one or two levels; the limit prevents hostile
// input from turning the guard into an unbounded parser recursion.
const maxShellNesting = 8

// Guard classifies commands against a configured risk threshold.
type Guard struct {
	threshold RiskLevel
	overrides map[string]string
	modifiers map[string][]ArgModifier
}

// NewGuard builds a Guard. The threshold defaults to "medium" when empty.
// Overrides and user modifiers are validated so a typo fails configuration
// loading instead of silently disabling the guard.
func NewGuard(
	threshold string,
	overrides map[string]string,
	modifiers map[string][]ArgModifier,
) (*Guard, error) {
	level, err := ParseRiskLevel(threshold)
	if err != nil {
		return nil, err
	}

	normalizedOverrides := NormalizeCommandKeys(overrides)
	for cmd, raw := range normalizedOverrides {
		if _, err := ParseRiskLevel(raw); err != nil {
			return nil, fmt.Errorf("invalid risk override for %q: %w", cmd, err)
		}
	}

	normalizedModifiers := make(map[string][]ArgModifier, len(modifiers))
	for cmd, entries := range modifiers {
		key := baseCommand(cmd)
		for _, entry := range entries {
			if entry.Level < RiskLow || entry.Level > RiskCritical {
				return nil, fmt.Errorf("invalid risk modifier level for %q", cmd)
			}
			if len(entry.Args) == 0 {
				return nil, fmt.Errorf("risk modifier for %q must list at least one argument", cmd)
			}
			normalizedModifiers[key] = append(normalizedModifiers[key], entry)
		}
	}

	return &Guard{
		threshold: level,
		overrides: normalizedOverrides,
		modifiers: normalizedModifiers,
	}, nil
}

// Check parses command and returns a BlockedError when any simple command in it
// exceeds the configured threshold. A nil result means the command may run.
func (g *Guard) Check(command string) *BlockedError {
	if g == nil {
		return nil
	}
	return g.checkScript(command, 0)
}

func (g *Guard) checkScript(command string, depth int) *BlockedError {
	if depth > maxShellNesting {
		return NewBlockedError(
			[]string{"<nested shell>"}, RiskCritical, g.threshold, "shell nesting limit exceeded",
		)
	}

	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return NewBlockedError(
			[]string{summarize(command)},
			RiskCritical,
			g.threshold,
			"command could not be parsed for risk classification",
		)
	}

	var blocked *BlockedError
	syntax.Walk(file, func(node syntax.Node) bool {
		if blocked != nil {
			return false
		}
		if node == nil {
			return true
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		blocked = g.checkCall(call, depth)
		return blocked == nil
	})
	return blocked
}

func (g *Guard) checkCall(call *syntax.CallExpr, depth int) *BlockedError {
	if len(call.Args) == 0 {
		return nil
	}

	words := make([]word, len(call.Args))
	for i, arg := range call.Args {
		words[i] = literalWord(arg)
	}

	first := words[0]
	if !first.literal || first.text == "" {
		return NewBlockedError(
			[]string{"<dynamic>"}, RiskCritical, g.threshold,
			"dynamic command name cannot be classified",
		)
	}

	name := baseCommand(first.text)
	if isShellCommand(name) {
		return g.checkShell(name, words, depth)
	}
	if name == "trap" {
		return g.checkTrap(words, depth)
	}
	if spec, ok := wrapperSpecs[name]; ok {
		return g.checkWrapper(name, words, spec, depth)
	}
	return g.classify(words)
}

func (g *Guard) classify(words []word) *BlockedError {
	args := wordTexts(words)
	level := ClassifyCommand(args, g.overrides, g.modifiers)
	if IsAllowed(level, g.threshold) {
		return nil
	}
	return NewBlockedError(args, level, g.threshold, "command risk exceeds configured threshold")
}

func (g *Guard) checkShell(name string, words []word, depth int) *BlockedError {
	if depth >= maxShellNesting {
		return NewBlockedError(
			wordTexts(words), RiskCritical, g.threshold, "shell nesting limit exceeded",
		)
	}

	script, ok := shellScriptArg(words[1:])
	if !ok {
		return NewBlockedError(
			wordTexts(words), RiskCritical, g.threshold,
			"shell reads commands from stdin or a script file that cannot be inspected",
		)
	}
	if !script.literal {
		return NewBlockedError(
			wordTexts(words), RiskCritical, g.threshold, "shell script argument is dynamic",
		)
	}
	return g.checkScript(script.text, depth+1)
}

func (g *Guard) checkTrap(words []word, depth int) *BlockedError {
	if depth >= maxShellNesting {
		return NewBlockedError(
			wordTexts(words), RiskCritical, g.threshold, "command nesting limit exceeded",
		)
	}

	for i := 1; i < len(words); i++ {
		w := words[i]
		if !w.literal {
			return NewBlockedError(
				wordTexts(words), RiskCritical, g.threshold, "trap action is dynamic",
			)
		}
		switch w.text {
		case "-l", "-p":
			return nil
		case "--":
			continue
		case "", "-":
			return nil
		}
		if strings.HasPrefix(w.text, "-") {
			continue
		}
		return g.checkScript(w.text, depth+1)
	}
	return nil
}

func (g *Guard) checkWrapper(name string, words []word, spec wrapperSpec, depth int) *BlockedError {
	if depth >= maxShellNesting {
		return NewBlockedError(
			wordTexts(words), RiskCritical, g.threshold, "command nesting limit exceeded",
		)
	}
	if name == "command" && hasCommandLookupFlag(words[1:]) {
		return nil
	}
	if name == "env" && hasEnvSplitString(words[1:]) {
		return NewBlockedError(
			wordTexts(words), RiskCritical, g.threshold,
			"env -S executes a command string that cannot be inspected",
		)
	}
	if blocked := g.classify(words); blocked != nil {
		return blocked
	}

	nested, found, dynamic := wrapperNested(words[1:], spec)
	if dynamic {
		return NewBlockedError(
			wordTexts(words), RiskCritical, g.threshold, "dynamic command name in "+name,
		)
	}
	if !found || len(nested) == 0 {
		return nil
	}

	nestedName := ""
	if nested[0].literal {
		nestedName = baseCommand(nested[0].text)
	}
	if nestedName == "" {
		return NewBlockedError(
			wordTexts(words), RiskCritical, g.threshold, "dynamic command name in "+name,
		)
	}
	if isShellCommand(nestedName) {
		if name == "xargs" {
			return NewBlockedError(
				wordTexts(words), RiskCritical, g.threshold,
				"xargs launching a shell cannot be inspected",
			)
		}
		return g.checkShell(nestedName, nested, depth+1)
	}
	if nestedName == "trap" {
		return g.checkTrap(nested, depth+1)
	}
	if next, ok := wrapperSpecs[nestedName]; ok {
		return g.checkWrapper(nestedName, nested, next, depth+1)
	}
	return g.classify(nested)
}

// word is a shell word reduced to its literal parts. literal is false when the
// word contains an expansion, substitution or glob, which the static guard
// cannot resolve.
type word struct {
	text    string
	literal bool
}

func literalWord(w *syntax.Word) word {
	var b strings.Builder
	literal := true

	var addParts func([]syntax.WordPart)
	addParts = func(parts []syntax.WordPart) {
		for _, part := range parts {
			switch p := part.(type) {
			case *syntax.Lit:
				b.WriteString(p.Value)
			case *syntax.SglQuoted:
				b.WriteString(p.Value)
				// $'...' is an ANSI-C quoted string: the parser keeps escapes
				// such as \x72 undecoded, so treating it as literal would let
				// $'\x72m' -rf / masquerade as an unknown command.
				if p.Dollar {
					literal = false
				}
			case *syntax.DblQuoted:
				addParts(p.Parts)
			default:
				literal = false
			}
		}
	}
	addParts(w.Parts)
	return word{text: b.String(), literal: literal}
}

func wordTexts(words []word) []string {
	args := make([]string, 0, len(words))
	for _, w := range words {
		if w.text != "" {
			args = append(args, w.text)
		}
	}
	return args
}

func summarize(s string) string {
	const maxLen = 120
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func isShellCommand(name string) bool {
	switch name {
	case "sh", "bash", "zsh", "dash", "fish", "csh", "tcsh", "ksh", "pwsh", "powershell", "cmd":
		return true
	}
	return false
}

// shellValueFlags are shell options that consume the following word, so it is
// not mistaken for the -c script or a script file.
var shellValueFlags = map[string]bool{
	"-o": true, "-O": true, "--rcfile": true, "--init-file": true,
}

// shellScriptArg extracts the literal -c script from a shell invocation. It
// returns ok=false when the shell would read commands from stdin or a script
// file, because those commands cannot be inspected statically.
func shellScriptArg(words []word) (word, bool) {
	for i := 0; i < len(words); i++ {
		w := words[i]
		if !w.literal {
			return word{}, false
		}
		text := w.text
		switch {
		case text == "--":
			return word{}, false
		case text == "--command":
			if i+1 < len(words) {
				return words[i+1], true
			}
			return word{}, false
		case strings.HasPrefix(text, "--command="):
			return word{text: strings.TrimPrefix(text, "--command="), literal: w.literal}, true
		case shellValueFlags[text]:
			i++
		case text == "-c":
			if i+1 < len(words) {
				return words[i+1], true
			}
			return word{}, false
		case strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "--"):
			if strings.Contains(text[1:], "c") {
				if i+1 < len(words) {
					return words[i+1], true
				}
				return word{}, false
			}
			if strings.ContainsAny(text[1:], "si") {
				return word{}, false
			}
		case !strings.HasPrefix(text, "-"):
			return word{}, false
		}
	}
	return word{}, false
}

type wrapperSpec struct {
	valueFlags     map[string]bool
	skipPositional int
	envAssignments bool
}

var wrapperSpecs = map[string]wrapperSpec{
	"env": {
		valueFlags: map[string]bool{
			"-u": true, "-C": true, "-S": true,
			"--unset": true, "--chdir": true, "--split-string": true,
		},
		envAssignments: true,
	},
	"command": {},
	"builtin": {},
	"nohup":   {},
	"time": {
		valueFlags: map[string]bool{
			"-o": true, "--output": true, "-f": true, "--format": true,
		},
	},
	"nice": {
		valueFlags: map[string]bool{"-n": true, "--adjustment": true},
	},
	"ionice": {
		valueFlags: map[string]bool{
			"-c": true, "-n": true, "-p": true,
			"--class": true, "--classdata": true, "--pid": true,
		},
	},
	"stdbuf": {
		valueFlags: map[string]bool{
			"-i": true, "-o": true, "-e": true,
			"--input": true, "--output": true, "--error": true,
		},
	},
	"timeout": {
		valueFlags: map[string]bool{
			"-s": true, "-k": true, "--signal": true, "--kill-after": true,
		},
		skipPositional: 1,
	},
	"watch": {
		valueFlags: map[string]bool{"-n": true, "--interval": true},
	},
	"setsid": {},
	"xvfb-run": {
		valueFlags: map[string]bool{
			"-e": true, "-n": true, "-s": true,
			"--error-file": true, "--server-num": true, "--server-args": true,
		},
	},
	"xargs": {
		valueFlags: map[string]bool{
			"-a": true, "-d": true, "-E": true, "-e": true, "-I": true,
			"-i": true, "-L": true, "-l": true, "-n": true, "-P": true,
			"-s": true, "--arg-file": true, "--delimiter": true,
			"--max-args": true, "--max-procs": true, "--max-chars": true,
			"--replace": true,
		},
	},
}

func wrapperNested(words []word, spec wrapperSpec) ([]word, bool, bool) {
	positional := spec.skipPositional

	for i := 0; i < len(words); i++ {
		w := words[i]
		if spec.envAssignments && isAssignment(w.text) {
			continue
		}
		if !w.literal {
			return nil, false, true
		}

		text := w.text
		if text == "--" {
			if i+1 < len(words) {
				return words[i+1:], true, false
			}
			return nil, false, false
		}
		if strings.HasPrefix(text, "-") && text != "-" {
			if spec.valueFlags[text] {
				i++
				continue
			}
			if name, _, ok := strings.Cut(text, "="); ok && spec.valueFlags[name] {
				continue
			}
			if len(text) > 2 && text[1] != '-' && spec.valueFlags[text[:2]] {
				continue
			}
			continue
		}
		if positional > 0 {
			positional--
			continue
		}
		return words[i:], true, false
	}
	return nil, false, false
}

func isAssignment(s string) bool {
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		c := s[i]
		switch {
		case c == '_':
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case i > 0 && c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return true
}

// hasCommandLookupFlag reports whether command(1) was invoked with -v/-V,
// including combined forms such as -pv. Lookup mode does not execute the
// argument, so it is not a guard concern.
func hasCommandLookupFlag(words []word) bool {
	for _, w := range words {
		if !w.literal {
			continue
		}
		text := w.text
		if strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "--") &&
			strings.ContainsAny(text[1:], "vV") {
			return true
		}
	}
	return false
}

// hasEnvSplitString reports whether an env invocation uses -S/--split-string.
// The attached form (-S"cmd") does not match the exact -S argument modifier,
// so it is checked here before the argv reaches the classifier.
func hasEnvSplitString(words []word) bool {
	for _, w := range words {
		if isAssignment(w.text) {
			continue
		}
		if !w.literal {
			return false
		}
		text := w.text
		if text == "--" || text == "-" {
			return false
		}
		if !strings.HasPrefix(text, "-") {
			return false
		}
		if text == "--split-string" || strings.HasPrefix(text, "--split-string=") {
			return true
		}
		if !strings.HasPrefix(text, "--") && shortEnvSplitFlag(text) {
			return true
		}
	}
	return false
}

// shortEnvSplitFlag scans an env short-option cluster. -u and -C consume the
// rest of the word as their value, so -uS unsets S rather than enabling -S.
func shortEnvSplitFlag(text string) bool {
	for i := 1; i < len(text); i++ {
		switch text[i] {
		case 'u', 'C':
			return false
		case 'S':
			return true
		}
	}
	return false
}
