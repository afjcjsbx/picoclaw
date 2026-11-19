package shellguard

import (
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
		return preferAssessment(result, unknownFor(words, l.opaque))
	}
	if l.bareShell {
		// A script file named on the command line stays opaque: its contents
		// are not read or classified, so the command is treated as unknown.
		return unknownFor(
			words, "shell reads commands from stdin or a script file that cannot be inspected",
		)
	}
	if len(l.scripts)+len(l.argvs) > 0 && !canNest(depth) {
		return unknownFor(words, "command nesting limit exceeded")
	}

	for _, script := range l.scripts {
		if script.dynamic {
			return preferAssessment(result, unknownFor(words, "script argument of "+l.name+" is dynamic"))
		}
		result = preferAssessment(result, g.assessScript(script.text, depth+1))
	}
	for _, argv := range l.argvs {
		if l.name == "xargs" && len(argv) > 0 && isShellCommand(baseCommand(argv[0].text)) {
			return unknownFor(words, "xargs launching a shell cannot be inspected")
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

// unknownFor marks the command made of words as unclassifiable.
func unknownFor(words []word, reason string) Assessment {
	return unknownAssessment(wordTexts(words), reason)
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
