package shellguard

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// maxShellNesting bounds recursive analysis of sh -c scripts and wrapper
// commands. Real commands nest one or two levels; the limit prevents hostile
// input from turning the guard into an unbounded parser recursion. It is the
// deepest level analyzed: canNest decides whether a launcher at some depth may
// descend, and every function that parses a nested script rejects a depth
// beyond it.
const maxShellNesting = 8

// Guard classifies commands against a configured risk threshold.
type Guard struct {
	threshold RiskLevel
}

// NewGuard builds a Guard. The threshold defaults to "medium" when empty.
func NewGuard(threshold string) (*Guard, error) {
	level, err := ParseRiskLevel(threshold)
	if err != nil {
		return nil, err
	}
	return &Guard{threshold: level}, nil
}

// Verdict says how far an Assessment can be trusted.
type Verdict int

const (
	// VerdictKnown means every command was classified and Level is the highest
	// risk among them.
	VerdictKnown Verdict = iota
	// VerdictUnknown marks a command that cannot be classified statically
	// (dynamic command names, opaque shells, or parse errors). Level is then
	// only a conservative placeholder; the default policy allows it.
	VerdictUnknown
	// VerdictReject marks a command that must be blocked even when the risk
	// level would otherwise be allowed. It is used for constructs the static
	// guards cannot validate, such as a dynamic operand to a destructive
	// command. Unlike VerdictUnknown, it is never allowed by the default policy.
	VerdictReject
)

// Assessment is the static risk evaluation of a command.
type Assessment struct {
	Level   RiskLevel
	Command string
	Reason  string
	Verdict Verdict
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
		Unknown:   a.Verdict == VerdictUnknown,
	}
}

// Check applies the default policy to a parsed command and returns nil when it
// may run. A rejected construct always blocks, a known command blocks above
// the threshold, and a command that cannot be classified is allowed: the
// caller's other layers still apply to it.
func (g *Guard) Check(command string, file *syntax.File) *BlockedError {
	if g == nil {
		return nil
	}
	assessment := g.AssessFile(command, file)
	switch assessment.Verdict {
	case VerdictUnknown:
		return nil
	case VerdictKnown:
		if IsAllowed(assessment.Level, g.threshold) {
			return nil
		}
	}
	return assessment.BlockedError(g.threshold)
}

// Threshold returns the highest risk level the guard allows.
func (g *Guard) Threshold() RiskLevel {
	if g == nil {
		return RiskLow
	}
	return g.threshold
}

// AssessFile classifies every simple command in an already parsed command and
// returns the highest risk it finds. It returns a result even when the command
// is allowed, so callers can apply their own threshold or approval policy. A
// nil file is treated as a parse failure.
func (g *Guard) AssessFile(command string, file *syntax.File) Assessment {
	if g == nil {
		return Assessment{}
	}
	if file == nil {
		return parseAssessment(command)
	}
	return g.assessFile(file, 0)
}

func parseAssessment(command string) Assessment {
	return unknownAssessment(
		[]string{summarize(command)},
		"command could not be parsed for risk classification",
	)
}

// assessScript parses and walks a literal shell script (sh -c, eval, trap or a
// wrapper command string).
func (g *Guard) assessScript(command string, depth int) Assessment {
	if depth > maxShellNesting {
		return unknownAssessment(
			[]string{"<nested shell>"}, "shell nesting limit exceeded",
		)
	}

	file, err := Parse(command)
	if err != nil {
		return unknownAssessment(
			[]string{summarize(command)},
			"command could not be parsed for risk classification",
		)
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
// CallExpr nodes. What the command launches comes from resolveLaunch; a
// launcher is rated by its own risk plus the highest risk of what it runs.
func (g *Guard) assessArgv(words []word, depth int) Assessment {
	if len(words) == 0 {
		return Assessment{}
	}

	l := resolveLaunch(words)
	if l.dynamicName {
		return unknownAssessment(
			[]string{"<dynamic>"}, "dynamic command name cannot be classified",
		)
	}

	var result Assessment
	if !l.transparent {
		result = g.classify(words)
	}
	if l.opaque != "" {
		return preferAssessment(result, unknownAssessment(wordTexts(words), l.opaque))
	}
	if l.bareShell {
		// A script file named on the command line stays opaque: its contents
		// are not read or classified, so the command is treated as unknown.
		return unknownAssessment(
			wordTexts(words),
			"shell reads commands from stdin or a script file that cannot be inspected",
		)
	}
	if len(l.scripts)+len(l.argvs) > 0 && !canNest(depth) {
		return unknownAssessment(wordTexts(words), "command nesting limit exceeded")
	}

	for _, script := range l.scripts {
		if script.dynamic {
			return preferAssessment(result, unknownAssessment(
				wordTexts(words), "script argument of "+l.name+" is dynamic",
			))
		}
		result = preferAssessment(result, g.assessScript(script.text, depth+1))
	}
	for _, argv := range l.argvs {
		if l.name == "xargs" && len(argv) > 0 && isShellCommand(baseCommand(argv[0].text)) {
			return unknownAssessment(
				wordTexts(words), "xargs launching a shell cannot be inspected",
			)
		}
		result = preferAssessment(result, g.assessArgv(argv, depth+1))
	}
	return result
}

func (g *Guard) classify(words []word) Assessment {
	if assessment, rejected := dynamicDestructiveOperand(words); rejected {
		return assessment
	}
	args := wordTexts(words)
	level, known := ClassifyCommand(args)
	if !known {
		return unknownAssessment(args, "command is not in the risk table")
	}
	return Assessment{
		Level:   level,
		Command: FormatCommand(args),
	}
}

func dynamicDestructiveOperand(words []word) (Assessment, bool) {
	if len(words) == 0 || words[0].dynamic {
		return Assessment{}, false
	}
	if !isDestructiveOperandCommand(baseCommand(words[0].text)) {
		return Assessment{}, false
	}
	for _, arg := range words[1:] {
		if arg.dynamic {
			return Assessment{
				Level:   RiskCritical,
				Command: FormatCommand(wordTexts(words)),
				Reason:  "dynamic operand for destructive command cannot be validated",
				Verdict: VerdictReject,
			}, true
		}
	}
	return Assessment{}, false
}

func unknownAssessment(args []string, reason string) Assessment {
	return Assessment{
		Level:   RiskCritical,
		Command: FormatCommand(args),
		Reason:  reason,
		Verdict: VerdictUnknown,
	}
}

// preferAssessment keeps the more serious of two assessments: a rejection
// beats an unknown command, which beats any known level, because an unresolved
// risk must not be masked by a numeric one. Between equals the higher level
// wins.
func preferAssessment(current, candidate Assessment) Assessment {
	switch {
	case candidate.Verdict != current.Verdict:
		if candidate.Verdict > current.Verdict {
			return candidate
		}
		return current
	case candidate.Level > current.Level:
		return candidate
	case current.Command == "" && candidate.Command != "":
		return candidate
	default:
		return current
	}
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
	commandFlags   map[string]bool
	skipPositional int
	envArgs        bool
	// shellAfterDoubleDash marks wrappers that execute /bin/bash with the
	// arguments after `--` (capsh). Those arguments are parsed as a script.
	shellAfterDoubleDash bool
}

// containerCLISpec is shared by lxc and incus: both take a subcommand, a
// container name and then the command to run inside the container.
var containerCLISpec = wrapperSpec{
	valueFlags: map[string]bool{
		"--project": true, "-P": true, "--remote": true,
		"--env": true, "--cwd": true, "--user": true,
		"--group": true, "--mode": true, "--width": true,
		"--height": true,
	},
	skipPositional: 2,
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
	"runuser": {
		valueFlags: map[string]bool{
			"-u": true, "--user": true, "-g": true, "--group": true,
			"-G": true, "--supp-group": true, "-s": true, "--shell": true,
			"-w": true, "--whitelist-environment": true,
		},
		commandFlags: map[string]bool{
			"-c": true, "--command": true, "--session-command": true,
		},
	},
	"setpriv": {
		valueFlags: map[string]bool{
			"--reuid": true, "--regid": true, "--groups": true,
			"--inh-caps": true, "--ambient-caps": true,
			"--bounding-set": true, "--securebits": true,
			"--selinux-label": true, "--apparmor-profile": true,
			"--landlock-access": true, "--landlock-rule": true,
		},
	},
	"capsh": {
		valueFlags: map[string]bool{
			"--user": true, "--group": true, "--uid": true, "--gid": true,
			"--shell": true, "--keep": true, "--caps": true, "--inh": true,
			"--addamb": true, "--delamb": true, "--drop": true,
			"--chroot": true, "--securebits": true,
		},
		shellAfterDoubleDash: true,
	},
	"machinectl": {
		valueFlags: map[string]bool{
			"-M": true, "--machine": true, "--uid": true, "--host": true,
			"-H": true, "-i": true, "--identity": true, "-p": true,
			"--property": true, "-n": true, "--lines": true, "-o": true,
			"--output": true, "-s": true, "--signal": true,
			"--kill-whom": true,
		},
		skipPositional: 2,
	},
	"lxc":   containerCLISpec,
	"incus": containerCLISpec,
	"flock": {
		valueFlags: map[string]bool{
			"-w": true, "--wait": true, "--timeout": true,
			"-E": true, "--conflict-exit-code": true,
		},
		commandFlags:   map[string]bool{"-c": true, "--command": true},
		skipPositional: 1,
	},
	"chrt": {
		valueFlags: map[string]bool{
			"-T": true, "--sched-runtime": true,
			"-P": true, "--sched-period": true,
			"-D": true, "--sched-deadline": true,
		},
		skipPositional: 1,
	},
	"taskset": {skipPositional: 1},
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

options:
	for i < len(args) {
		w := args[i]
		text := w.text
		stop := false
		switch {
		case isAssignment(text):
			out.assignments = append(out.assignments, w)
			i++
		case w.dynamic:
			out.dynamic = true
			return out
		case text == "--":
			i++
			break options
		case text == "-":
			out.ignoreEnv = true
			i++
		case !strings.HasPrefix(text, "-"):
			break options
		case strings.HasPrefix(text, "--"):
			i, stop = out.longOption(args, i)
		default:
			i, stop = out.shortOptions(args, i)
		}
		if stop {
			return out
		}
	}

	for i < len(args) && !args[i].dynamic && isAssignment(args[i].text) {
		out.assignments = append(out.assignments, args[i])
		i++
	}
	if i < len(args) {
		out.command = args[i:]
	}
	return out
}

// longOption parses the --option at args[i]. It returns the index of the next
// word and whether parsing is over: a split string swallows the rest, and an
// option missing its value makes the command unresolvable.
func (o *envArgs) longOption(args []word, i int) (next int, done bool) {
	text := args[i].text
	switch {
	case text == "--ignore-environment":
		o.ignoreEnv = true
	case text == "--unset":
		if i+1 >= len(args) {
			o.dynamic = true
			return i, true
		}
		i++
		o.addUnset(args[i])
	case strings.HasPrefix(text, "--unset="):
		o.unset = append(o.unset, strings.TrimPrefix(text, "--unset="))
	case text == "--chdir":
		if i+1 >= len(args) {
			o.dynamic = true
			return i, true
		}
		i++
		o.chdir, o.hasChdir = args[i], true
	case strings.HasPrefix(text, "--chdir="):
		o.chdir, o.hasChdir = word{text: strings.TrimPrefix(text, "--chdir=")}, true
	case text == "--split-string":
		o.splitString = true
		if i+1 < len(args) {
			o.splitValue = args[i+1]
		}
		return i, true
	case strings.HasPrefix(text, "--split-string="):
		o.splitString = true
		o.splitValue = word{text: strings.TrimPrefix(text, "--split-string=")}
		return i, true
	}
	// --null and unknown long options take no value.
	return i + 1, false
}

// shortOptions parses the clustered -iu... options at args[i], with the same
// result as longOption.
func (o *envArgs) shortOptions(args []word, i int) (next int, done bool) {
	text := args[i].text
	for j := 1; j < len(text); j++ {
		switch text[j] {
		case 'i':
			o.ignoreEnv = true
		case 'u', 'C':
			value, hasValue := optionValue(text, j+1, args, i)
			if !hasValue {
				o.dynamic = true
				return i, true
			}
			if text[j] == 'u' {
				o.addUnset(value.word)
			} else {
				o.chdir, o.hasChdir = value.word, true
			}
			if value.next {
				i++
			}
			j = len(text) - 1
		case 'S':
			o.splitString = true
			if value, hasValue := optionValue(text, j+1, args, i); hasValue {
				o.splitValue = value.word
			}
			return i, true
		}
	}
	return i + 1, false
}

func (o *envArgs) addUnset(name word) {
	if name.dynamic {
		o.unsetDynamic = true
		return
	}
	o.unset = append(o.unset, name.text)
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
