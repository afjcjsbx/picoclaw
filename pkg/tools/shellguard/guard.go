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

// Assessment is the static risk evaluation of a command. Unknown marks a
// command that cannot be classified statically (dynamic command names, opaque
// shells, or parse errors); smart approval routes those to the Guardian model.
type Assessment struct {
	Level   RiskLevel
	Command string
	Reason  string
	Unknown bool
}

// BlockedError converts an assessment into the structured error returned by
// Check, using the given threshold for the message.
func (a Assessment) BlockedError(threshold RiskLevel) *BlockedError {
	reason := a.Reason
	if reason == "" {
		reason = "command risk exceeds configured threshold"
	}
	return &BlockedError{
		Command:   a.Command,
		Level:     a.Level,
		Threshold: threshold,
		Reason:    reason,
		Unknown:   a.Unknown,
	}
}

// Threshold returns the highest risk level the guard allows.
func (g *Guard) Threshold() RiskLevel {
	if g == nil {
		return RiskLow
	}
	return g.threshold
}

// Assess classifies every simple command in command and returns the highest
// risk it finds. Unlike Check, it returns a result even when the command is
// allowed, so callers can apply an additional approval threshold.
func (g *Guard) Assess(command string) Assessment {
	if g == nil {
		return Assessment{}
	}
	return g.assessScript(command, 0)
}

// Check parses command and returns a BlockedError when any simple command in it
// exceeds the configured threshold. A nil result means the command may run.
func (g *Guard) Check(command string) *BlockedError {
	if g == nil {
		return nil
	}
	return g.checkAssessment(g.Assess(command))
}

func (g *Guard) checkAssessment(assessment Assessment) *BlockedError {
	if !assessment.Unknown && IsAllowed(assessment.Level, g.threshold) {
		return nil
	}
	return assessment.BlockedError(g.threshold)
}

// WithThreshold returns a shallow copy of the guard using a different maximum
// allowed level. It is used to measure a command against an operator's
// risk-allow threshold without mutating the shared guard.
func (g *Guard) WithThreshold(threshold RiskLevel) *Guard {
	if g == nil {
		return nil
	}
	clone := *g
	clone.threshold = threshold
	return &clone
}

func (g *Guard) assessScript(command string, depth int) Assessment {
	if depth > maxShellNesting {
		return unknownAssessment(
			[]string{"<nested shell>"}, "shell nesting limit exceeded",
		)
	}

	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return unknownAssessment(
			[]string{summarize(command)},
			"command could not be parsed for risk classification",
		)
	}

	var result Assessment
	syntax.Walk(file, func(node syntax.Node) bool {
		if node == nil {
			return true
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		result = preferAssessment(result, g.assessCall(call, depth))
		return true
	})
	return result
}

func (g *Guard) assessCall(call *syntax.CallExpr, depth int) Assessment {
	if len(call.Args) == 0 {
		return Assessment{}
	}

	words := make([]word, len(call.Args))
	for i, arg := range call.Args {
		words[i] = literalWord(arg)
	}
	return g.assessArgv(words, depth)
}

// assessArgv classifies one argument vector. It is shared by AST calls and by
// commands launched from find -exec/-ok, which are argv slices rather than
// CallExpr nodes.
func (g *Guard) assessArgv(words []word, depth int) Assessment {
	if len(words) == 0 {
		return Assessment{}
	}

	first := words[0]
	if !first.literal || first.text == "" {
		return unknownAssessment(
			[]string{"<dynamic>"}, "dynamic command name cannot be classified",
		)
	}

	name := baseCommand(first.text)
	if isShellCommand(name) {
		return g.assessShell(name, words, depth)
	}
	if name == "trap" {
		return g.assessTrap(words, depth)
	}
	if spec, ok := wrapperSpecs[name]; ok {
		return g.assessWrapper(name, words, spec, depth)
	}
	if name == "find" {
		return preferAssessment(g.classify(words), g.assessFindExec(words[1:], depth))
	}
	return g.classify(words)
}

// assessFindExec classifies commands launched by find -exec/-execdir/-ok/-okdir.
// find runs them directly, so a nested critical command must not hide behind
// find's own high-risk base level.
func (g *Guard) assessFindExec(words []word, depth int) Assessment {
	if depth >= maxShellNesting {
		return unknownAssessment(
			wordTexts(words), "command nesting limit exceeded",
		)
	}
	var result Assessment
	for i := 0; i < len(words); i++ {
		switch words[i].text {
		case "-exec", "-execdir", "-ok", "-okdir":
			end := i + 1
			for end < len(words) && words[end].text != ";" && words[end].text != "+" {
				end++
			}
			if end > i+1 {
				result = preferAssessment(result, g.assessArgv(words[i+1:end], depth+1))
			}
			i = end
		}
	}
	return result
}

func (g *Guard) classify(words []word) Assessment {
	args := wordTexts(words)
	return Assessment{
		Level:   ClassifyCommand(args, g.overrides, g.modifiers),
		Command: FormatCommand(args),
	}
}

func (g *Guard) assessShell(name string, words []word, depth int) Assessment {
	if depth >= maxShellNesting {
		return unknownAssessment(
			wordTexts(words), "shell nesting limit exceeded",
		)
	}

	script, ok := shellScriptArg(words[1:])
	if !ok {
		return unknownAssessment(
			wordTexts(words),
			"shell reads commands from stdin or a script file that cannot be inspected",
		)
	}
	if !script.literal {
		return unknownAssessment(
			wordTexts(words), "shell script argument is dynamic",
		)
	}
	return g.assessScript(script.text, depth+1)
}

func (g *Guard) assessTrap(words []word, depth int) Assessment {
	if depth >= maxShellNesting {
		return unknownAssessment(
			wordTexts(words), "command nesting limit exceeded",
		)
	}

	var result Assessment
	for i := 1; i < len(words); i++ {
		w := words[i]
		if !w.literal {
			return unknownAssessment(
				wordTexts(words), "trap action is dynamic",
			)
		}
		switch w.text {
		case "-l", "-p":
			return result
		case "--":
			continue
		case "", "-":
			return result
		}
		if strings.HasPrefix(w.text, "-") {
			continue
		}
		return g.assessScript(w.text, depth+1)
	}
	return result
}

func (g *Guard) assessWrapper(name string, words []word, spec wrapperSpec, depth int) Assessment {
	if depth >= maxShellNesting {
		return unknownAssessment(
			wordTexts(words), "command nesting limit exceeded",
		)
	}
	if name == "command" && hasCommandLookupFlag(words[1:]) {
		return Assessment{}
	}
	if name == "env" && hasEnvSplitString(words[1:]) {
		return unknownAssessment(
			wordTexts(words),
			"env -S executes a command string that cannot be inspected",
		)
	}

	result := g.classify(words)

	nested, found, dynamic := wrapperNested(words[1:], spec)
	if dynamic {
		return unknownAssessment(
			wordTexts(words), "dynamic command name in "+name,
		)
	}
	if !found || len(nested) == 0 {
		return result
	}

	nestedName := ""
	if nested[0].literal {
		nestedName = baseCommand(nested[0].text)
	}
	if nestedName == "" {
		return unknownAssessment(
			wordTexts(words), "dynamic command name in "+name,
		)
	}
	if isShellCommand(nestedName) {
		if name == "xargs" {
			return unknownAssessment(
				wordTexts(words),
				"xargs launching a shell cannot be inspected",
			)
		}
		return preferAssessment(result, g.assessShell(nestedName, nested, depth+1))
	}
	if nestedName == "trap" {
		return preferAssessment(result, g.assessTrap(nested, depth+1))
	}
	if next, ok := wrapperSpecs[nestedName]; ok {
		return preferAssessment(result, g.assessWrapper(nestedName, nested, next, depth+1))
	}
	return preferAssessment(result, g.classify(nested))
}

func unknownAssessment(args []string, reason string) Assessment {
	return Assessment{
		Level:   RiskCritical,
		Command: FormatCommand(args),
		Reason:  reason,
		Unknown: true,
	}
}

// preferAssessment keeps the highest known risk, but always lets an unknown
// command win: it needs the Guardian regardless of the numeric level.
func preferAssessment(current, candidate Assessment) Assessment {
	switch {
	case candidate.Unknown && !current.Unknown:
		return candidate
	case current.Unknown && !candidate.Unknown:
		return current
	case candidate.Level > current.Level:
		return candidate
	case current.Command == "" && candidate.Command != "":
		return candidate
	default:
		return current
	}
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
	// The first positional is the applet itself, so it is the nested command
	// name rather than an argument to skip.
	"busybox": {},
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
//
// Only the options that precede command's own argument count: a -v after the
// command name belongs to the nested command (`command rm -v`), not to
// command(1).
func hasCommandLookupFlag(words []word) bool {
	for _, w := range words {
		if !w.literal {
			return false
		}
		text := w.text
		if text == "--" || text == "-" || !strings.HasPrefix(text, "-") {
			return false
		}
		if !strings.HasPrefix(text, "--") && strings.ContainsAny(text[1:], "vV") {
			return true
		}
	}
	return false
}

// hasEnvSplitString reports whether an env invocation uses -S/--split-string.
// The attached form (-S"cmd") does not match the exact -S argument modifier,
// so it is checked here before the argv reaches the classifier.
//
// Options are scanned in order because -u and -C consume the following word.
// Without that, env -u NAME -S "cmd" hid the split string behind NAME.
func hasEnvSplitString(words []word) bool {
	for i := 0; i < len(words); i++ {
		w := words[i]
		if !w.literal {
			return false
		}
		text := w.text
		if isAssignment(text) || text == "-" {
			// "-" is env's short form of -i; both may be followed by options.
			continue
		}
		if text == "--" {
			return false
		}
		if !strings.HasPrefix(text, "-") {
			return false
		}
		if text == "--split-string" || strings.HasPrefix(text, "--split-string=") {
			return true
		}
		if strings.HasPrefix(text, "--unset") || strings.HasPrefix(text, "--chdir") {
			if !strings.Contains(text, "=") {
				i++ // the value is the next word
			}
			continue
		}
		if strings.HasPrefix(text, "--") {
			continue
		}
		for j := 1; j < len(text); j++ {
			switch text[j] {
			case 'u', 'C':
				if j == len(text)-1 {
					i++ // the value is the next word
				}
				j = len(text) - 1 // an attached value consumes the rest
			case 'S':
				return true
			}
		}
	}
	return false
}
