package shellguard

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// maxShellNesting bounds recursive analysis of sh -c scripts and wrapper
// commands. Real commands nest one or two levels; the limit prevents hostile
// input from turning the guard into an unbounded parser recursion.
//
// Depth N is the deepest level analyzed, and two comparisons enforce that on
// purpose. A function entered at some depth (assessScript, checkWorkspaceText,
// checkSimple, canonicalCommands) rejects depth > maxShellNesting. A helper
// about to recurse at depth+1 (assessShell, assessWrapper, assessTrap,
// assessFindExec, appendCanonicalCall) stops at depth >= maxShellNesting. Both
// refuse the same hop from N to N+1, so switching either side to the other
// operator moves the limit by one level on that path only.
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
	file, err := Parse(command)
	if err != nil {
		return parseAssessment(command)
	}
	return g.AssessFile(command, file)
}

// AssessFile is Assess for an already parsed command; a nil file is treated as
// a parse failure.
func (g *Guard) AssessFile(command string, file *syntax.File) Assessment {
	if g == nil {
		return Assessment{}
	}
	if file == nil {
		return parseAssessment(command)
	}
	return g.assessFile(file, 0)
}

// Check parses command and returns a BlockedError when any simple command in it
// exceeds the configured threshold. A nil result means the command may run.
func (g *Guard) Check(command string) *BlockedError {
	if g == nil {
		return nil
	}
	return g.checkAssessment(g.Assess(command))
}

// CheckFile classifies an already parsed command. It is the shared-parse entry
// point used by the exec guard; a nil file is treated as a parse failure.
func (g *Guard) CheckFile(command string, file *syntax.File) *BlockedError {
	if g == nil {
		return nil
	}
	return g.checkAssessment(g.AssessFile(command, file))
}

func (g *Guard) checkAssessment(assessment Assessment) *BlockedError {
	if !assessment.Unknown && IsAllowed(assessment.Level, g.threshold) {
		return nil
	}
	return assessment.BlockedError(g.threshold)
}

func parseAssessment(command string) Assessment {
	return unknownAssessment(
		[]string{summarize(command)},
		"command could not be parsed for risk classification",
	)
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

	file, err := Parse(command)
	if err != nil {
		return parseAssessment(command)
	}
	return g.assessFile(file, depth)
}

func (g *Guard) assessFile(file *syntax.File, depth int) Assessment {
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
	if first.dynamic || first.text == "" {
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
	if name == "exec" {
		result := g.classify(words)
		if nested, ok := execCommand(words[1:]); ok {
			result = preferAssessment(result, g.assessArgv(nested, depth+1))
		}
		return result
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
	for _, nested := range findExecCommands(words) {
		result = preferAssessment(result, g.assessArgv(nested, depth+1))
	}
	return result
}

// findExecCommands returns the argument vectors launched by find -exec,
// -execdir, -ok and -okdir.
func findExecCommands(words []word) [][]word {
	var commands [][]word
	for i := 0; i < len(words); i++ {
		switch words[i].text {
		case "-exec", "-execdir", "-ok", "-okdir":
			end := i + 1
			for end < len(words) && words[end].text != ";" && words[end].text != "+" {
				end++
			}
			if end > i+1 {
				commands = append(commands, words[i+1:end])
			}
			i = end
		}
	}
	return commands
}

// trapAction extracts the literal action from a trap argument vector. found is
// false for listing and reset forms; dynamic reports that the action or an
// option could not be resolved.
func trapAction(args []word) (action word, found, dynamic bool) {
	for _, w := range args {
		if w.dynamic {
			return word{}, false, true
		}
		switch w.text {
		case "-l", "-p":
			return word{}, false, false
		case "--":
			continue
		case "", "-":
			return word{}, false, false
		}
		if strings.HasPrefix(w.text, "-") {
			continue
		}
		return w, true, false
	}
	return word{}, false, false
}

// execCommand returns the argv that exec replaces the shell with, skipping its
// own options.
func execCommand(args []word) ([]word, bool) {
	i := 0
	for i < len(args) {
		text := args[i].text
		switch {
		case text == "--":
			i++
		case text == "-a":
			i += 2
			continue
		case strings.HasPrefix(text, "-a") && len(text) > 2:
			i++
			continue
		case text == "-c" || text == "-l":
			i++
			continue
		case strings.HasPrefix(text, "-") && text != "-":
			i++
			continue
		}
		break
	}
	if i >= len(args) {
		return nil, false
	}
	return args[i:], true
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

	invocation := parseShellInvocation(words[1:])
	if !invocation.hasScript {
		return unknownAssessment(
			wordTexts(words),
			"shell reads commands from stdin or a script file that cannot be inspected",
		)
	}
	if invocation.script.dynamic {
		return unknownAssessment(
			wordTexts(words), "shell script argument is dynamic",
		)
	}
	return g.assessScript(invocation.script.text, depth+1)
}

func (g *Guard) assessTrap(words []word, depth int) Assessment {
	if depth >= maxShellNesting {
		return unknownAssessment(
			wordTexts(words), "command nesting limit exceeded",
		)
	}

	action, found, dynamic := trapAction(words[1:])
	if dynamic {
		return unknownAssessment(
			wordTexts(words), "trap action is dynamic",
		)
	}
	if !found {
		return Assessment{}
	}
	return g.assessScript(action.text, depth+1)
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
	if !nested[0].dynamic {
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

// word is a shell word reduced to its literal parts. dynamic reports that the
// word contains an expansion, substitution or glob, which the static guard
// cannot resolve. homePath marks a word that starts with a tilde or $HOME.
type word struct {
	text     string
	dynamic  bool
	homePath bool
}

func literalWord(w *syntax.Word) word {
	// A single plain or single-quoted part is already the final argv word.
	// expand.Literal would produce the same text through a full expansion
	// config, so the direct path avoids those allocations for the common case.
	if len(w.Parts) == 1 {
		switch p := w.Parts[0].(type) {
		case *syntax.Lit:
			return word{text: p.Value}
		case *syntax.SglQuoted:
			if !p.Dollar {
				return word{text: p.Value}
			}
		}
	}

	var b strings.Builder
	dynamic := false

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
					dynamic = true
				}
			case *syntax.DblQuoted:
				addParts(p.Parts)
			default:
				dynamic = true
			}
		}
	}
	addParts(w.Parts)
	return word{text: b.String(), dynamic: dynamic}
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

// shellInvocation is the statically known script source of a shell invocation.
// hasScript reports a literal -c/--command argument. readsStdin reports that
// the shell would execute stdin, so a heredoc on the command is a script.
// opaque reports a script file or an unresolved argument that cannot be
// inspected statically.
type shellInvocation struct {
	script     word
	hasScript  bool
	readsStdin bool
	opaque     bool
}

// parseShellInvocation extracts the -c script or stdin mode from a shell
// argument vector. The guards share it so risk classification, workspace
// validation and canonical deny forms agree on shell flags.
func parseShellInvocation(words []word) shellInvocation {
	invocation := shellInvocation{readsStdin: true}
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w.dynamic {
			return shellInvocation{opaque: true}
		}
		text := w.text
		switch {
		case text == "--":
			if i+1 < len(words) {
				return shellInvocation{opaque: true}
			}
			return invocation
		case text == "-":
			return invocation
		case text == "--command":
			if i+1 < len(words) {
				return shellInvocation{script: words[i+1], hasScript: true}
			}
			return shellInvocation{opaque: true}
		case strings.HasPrefix(text, "--command="):
			return shellInvocation{
				script:    word{text: strings.TrimPrefix(text, "--command="), dynamic: w.dynamic},
				hasScript: true,
			}
		case shellValueFlags[text]:
			i++
		case text == "-c":
			if i+1 < len(words) {
				return shellInvocation{script: words[i+1], hasScript: true}
			}
			return shellInvocation{opaque: true}
		case strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "--"):
			flags := text[1:]
			if strings.Contains(flags, "c") {
				if i+1 < len(words) {
					return shellInvocation{script: words[i+1], hasScript: true}
				}
				return shellInvocation{opaque: true}
			}
			if strings.Contains(flags, "s") {
				return invocation
			}
		case !strings.HasPrefix(text, "-"):
			return shellInvocation{opaque: true}
		}
	}
	return invocation
}

type wrapperSpec struct {
	valueFlags     map[string]bool
	skipPositional int
	envArgs        bool
}

var wrapperSpecs = map[string]wrapperSpec{
	"env":     {envArgs: true},
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
	if spec.envArgs {
		opts := parseEnvArgs(words)
		return opts.command, len(opts.command) > 0, opts.dynamic
	}

	positional := spec.skipPositional
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w.dynamic {
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
		if w.dynamic {
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

// envArgs is the statically parsed surface of an env invocation.
type envArgs struct {
	command      []word
	assignments  []word
	chdir        word
	hasChdir     bool
	unset        []string
	unsetDynamic bool
	ignoreEnv    bool
	splitString  bool
	splitValue   word
	dynamic      bool
}

// parseEnvArgs parses env options once. It knows which options consume a value,
// so -u NAME -S "cmd" and -C DIR -S "cmd" still expose the split string.
func parseEnvArgs(args []word) envArgs {
	var out envArgs
	i := 0
	for i < len(args) {
		w := args[i]
		text := w.text
		if isAssignment(text) {
			out.assignments = append(out.assignments, w)
			i++
			continue
		}
		if w.dynamic {
			out.dynamic = true
			return out
		}
		switch {
		case text == "--":
			i++
			goto command
		case text == "-":
			out.ignoreEnv = true
			i++
			continue
		case !strings.HasPrefix(text, "-"):
			goto command
		case strings.HasPrefix(text, "--"):
			switch {
			case text == "--ignore-environment":
				out.ignoreEnv = true
			case text == "--null":
				// no value
			case text == "--unset":
				if i+1 >= len(args) {
					out.dynamic = true
					return out
				}
				i++
				if args[i].dynamic {
					out.unsetDynamic = true
				} else {
					out.unset = append(out.unset, args[i].text)
				}
			case strings.HasPrefix(text, "--unset="):
				out.unset = append(out.unset, strings.TrimPrefix(text, "--unset="))
			case text == "--chdir":
				if i+1 >= len(args) {
					out.dynamic = true
					return out
				}
				i++
				out.chdir = args[i]
				out.hasChdir = true
			case strings.HasPrefix(text, "--chdir="):
				out.chdir = word{text: strings.TrimPrefix(text, "--chdir=")}
				out.hasChdir = true
			case text == "--split-string":
				out.splitString = true
				if i+1 < len(args) {
					out.splitValue = args[i+1]
				}
				return out
			case strings.HasPrefix(text, "--split-string="):
				out.splitString = true
				out.splitValue = word{
					text: strings.TrimPrefix(text, "--split-string="),
				}
				return out
			}
			i++
			continue
		}

		for j := 1; j < len(text); j++ {
			switch text[j] {
			case 'i':
				out.ignoreEnv = true
			case '0':
				// no value
			case 'u':
				value, hasValue := optionValue(text, j+1, args, i)
				if !hasValue {
					out.dynamic = true
					return out
				}
				if !value.word.dynamic {
					out.unset = append(out.unset, value.word.text)
				} else {
					out.unsetDynamic = true
				}
				if value.next {
					i++
				}
				j = len(text) - 1
			case 'C':
				value, hasValue := optionValue(text, j+1, args, i)
				if !hasValue {
					out.dynamic = true
					return out
				}
				out.chdir = value.word
				out.hasChdir = true
				if value.next {
					i++
				}
				j = len(text) - 1
			case 'S':
				out.splitString = true
				value, hasValue := optionValue(text, j+1, args, i)
				if hasValue {
					out.splitValue = value.word
				}
				return out
			}
		}
		i++
	}

command:
	for i < len(args) && !args[i].dynamic && isAssignment(args[i].text) {
		out.assignments = append(out.assignments, args[i])
		i++
	}
	if i < len(args) {
		out.command = args[i:]
	}
	return out
}

type envOptionValue struct {
	word word
	next bool
}

// optionValue returns the value of a short env option. The rest of the option
// word is an attached value; otherwise the next word is the value.
func optionValue(text string, rest int, args []word, index int) (envOptionValue, bool) {
	if rest < len(text) {
		return envOptionValue{word: word{text: text[rest:]}}, true
	}
	if index+1 >= len(args) {
		return envOptionValue{}, false
	}
	return envOptionValue{word: args[index+1], next: true}, true
}

func hasEnvSplitString(words []word) bool {
	return parseEnvArgs(words).splitString
}
