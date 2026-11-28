package shellguard

import (
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// constEnv follows plain VAR=literal assignments and `set --` through the
// straight-line top of a script, so a later $VAR or "$@" resolves to the text
// the shell will run: `R=rm; $R -rf x` is classified as rm -rf x instead of a
// dynamic command name.
//
// The tracking is deliberately small and fails toward the old behavior. The
// first construct that could change variables behind its back (functions,
// eval, read, a loop, a conditional assignment, ...) turns it off for the rest
// of the script, and words that no longer resolve stay dynamic, which the guard
// already treats as unknown.
type constEnv struct {
	vars     map[string]string
	pos      []string
	posKnown bool
	// cond is non-zero inside the right side of && and ||, in a pipeline and
	// in a background job: an assignment there may or may not take effect.
	cond int
	off  bool
	// resolved holds the argv of every call whose words used an expansion.
	resolved map[*syntax.CallExpr][]word
}

// resolveConstants returns, for each top-level simple command of file whose
// words expand a tracked variable or positional parameter, its argv with those
// expansions replaced by their value. Commands it cannot follow are absent and
// are read as written.
func resolveConstants(file *syntax.File) map[*syntax.CallExpr][]word {
	var e constEnv
	for _, st := range file.Stmts {
		e.stmt(st)
	}
	return e.resolved
}

func (e *constEnv) stmt(st *syntax.Stmt) {
	if e.off {
		return
	}
	if st.Coprocess {
		e.off = true
		return
	}
	for _, rd := range st.Redirs {
		if hasAssignExpansion(rd) {
			e.off = true
			return
		}
	}
	if st.Background {
		e.cond++
		defer func() { e.cond-- }()
	}

	switch c := st.Cmd.(type) {
	case *syntax.CallExpr:
		e.call(c)
	case *syntax.DeclClause:
		e.decl(c)
	case *syntax.BinaryCmd:
		e.binary(c)
	default:
		e.off = true
	}
}

func (e *constEnv) binary(c *syntax.BinaryCmd) {
	switch c.Op {
	case syntax.AndStmt, syntax.OrStmt:
		e.stmt(c.X)
		e.cond++
		e.stmt(c.Y)
		e.cond--
	case syntax.Pipe, syntax.PipeAll:
		e.cond++
		e.stmt(c.X)
		e.stmt(c.Y)
		e.cond--
	default:
		e.off = true
	}
}

func (e *constEnv) call(c *syntax.CallExpr) {
	if hasAssignExpansion(c) {
		e.off = true
		return
	}
	if len(c.Args) == 0 {
		for _, a := range c.Assigns {
			e.assign(a)
		}
		return
	}

	words, changed := e.expand(c.Args)
	if changed {
		if e.resolved == nil {
			e.resolved = make(map[*syntax.CallExpr][]word)
		}
		e.resolved[c] = words
	}
	// A prefix assignment lasts for this command, or longer when the command is
	// a special builtin, depending on the shell. Forgetting it is safe either way.
	for _, a := range c.Assigns {
		e.forget(a)
	}
	e.builtin(words)
}

// decl follows `export NAME=value`. Every other declaration form can add
// attributes (readonly, nameref, case folding) that change later assignments.
func (e *constEnv) decl(c *syntax.DeclClause) {
	if c.Variant.Value != "export" || hasAssignExpansion(c) {
		e.off = true
		return
	}
	for _, a := range c.Args {
		if a.Name == nil {
			e.off = true
			return
		}
		if !a.Naked {
			e.assign(a)
		}
	}
}

// untrackedVar reports a variable the shell changes by itself, protects from
// assignment, or reads while splitting words, so a recorded value cannot be
// trusted.
func untrackedVar(name string) bool {
	switch name {
	case "PWD", "OLDPWD", "RANDOM", "SRANDOM", "SECONDS", "LINENO", "UID", "EUID",
		"PPID", "OPTIND", "OPTARG", "REPLY", "SHELLOPTS", "_":
		return true
	}
	return strings.HasPrefix(name, "BASH") || strings.HasPrefix(name, "EPOCH")
}

func (e *constEnv) assign(a *syntax.Assign) {
	name := a.Name.Value
	if name == "IFS" {
		// A custom IFS changes how every unquoted expansion splits.
		e.off = true
		return
	}
	if untrackedVar(name) {
		return
	}
	if e.cond > 0 || a.Append || a.Naked || a.Array != nil || a.Index != nil {
		delete(e.vars, name)
		return
	}

	var w word
	if a.Value != nil {
		w = buildWord(a.Value, &wordExpander{env: e}, true)
	}
	// A tilde would expand to a home directory the guard does not know.
	if w.dynamic || strings.Contains(w.text, "~") {
		delete(e.vars, name)
		return
	}
	if e.vars == nil {
		e.vars = make(map[string]string)
	}
	e.vars[name] = w.text
}

func (e *constEnv) forget(a *syntax.Assign) {
	if a.Name.Value == "IFS" {
		e.off = true
		return
	}
	delete(e.vars, a.Name.Value)
}

// expand builds the argv of a command, resolving tracked expansions. changed
// reports that at least one word used an expansion.
func (e *constEnv) expand(args []*syntax.Word) (words []word, changed bool) {
	x := &wordExpander{env: e}
	words = make([]word, 0, len(args))
	for _, arg := range args {
		if !hasParam(arg) {
			words = append(words, literalWord(arg))
			continue
		}
		changed = true
		if e.posKnown && isAllPositional(arg) {
			words = e.appendPositional(words, arg)
			continue
		}
		words = append(words, buildWord(arg, x, false))
	}
	return words, changed
}

// isAllPositional reports a word that is exactly $@ or "$@".
func isAllPositional(w *syntax.Word) bool {
	if len(w.Parts) != 1 {
		return false
	}
	part := w.Parts[0]
	if dq, ok := part.(*syntax.DblQuoted); ok && len(dq.Parts) == 1 {
		part = dq.Parts[0]
	}
	p, ok := part.(*syntax.ParamExp)
	if !ok {
		return false
	}
	name, ok := simpleParamName(p)
	return ok && name == "@"
}

// appendPositional expands $@ into one word per positional parameter. Unquoted,
// an empty parameter disappears and one that would split or glob stays dynamic.
func (e *constEnv) appendPositional(words []word, w *syntax.Word) []word {
	_, quoted := w.Parts[0].(*syntax.DblQuoted)
	for _, v := range e.pos {
		switch {
		case quoted:
			words = append(words, word{text: v, expanded: true})
		case v != "":
			words = append(words, word{text: v, dynamic: splitsOrGlobs(v), expanded: true})
		}
	}
	return words
}

// builtin applies the effect a resolved command has on the tracked state.
// External commands cannot change the shell's variables, so only builtins and
// anything that might be a function matter.
func (e *constEnv) builtin(words []word) {
	if len(words) == 0 || words[0].dynamic {
		e.off = true
		return
	}
	switch words[0].text {
	case "set":
		e.setPositional(words[1:])
	case "shift":
		e.shift(words[1:])
	case "unset":
		e.unset(words[1:])
	case "eval", ".", "source", "exec", "command", "builtin", "trap", "alias", "unalias",
		"read", "mapfile", "readarray", "getopts", "let", "printf", "wait", "fc", "enable",
		"pushd", "popd", "declare", "typeset", "local", "readonly", "export":
		e.off = true
	}
}

func (e *constEnv) setPositional(args []word) {
	if len(args) == 0 {
		return
	}
	e.posKnown = false
	if e.cond > 0 || args[0].dynamic || args[0].text != "--" {
		return
	}
	pos := make([]string, 0, len(args)-1)
	for _, w := range args[1:] {
		if w.dynamic {
			return
		}
		pos = append(pos, w.text)
	}
	e.pos, e.posKnown = pos, true
}

func (e *constEnv) shift(args []word) {
	if !e.posKnown {
		return
	}
	n := 1
	if len(args) > 0 {
		var err error
		if n, err = strconv.Atoi(args[0].text); err != nil || args[0].dynamic {
			n = -1
		}
	}
	if e.cond > 0 || n < 0 || n > len(e.pos) {
		e.posKnown = false
		return
	}
	e.pos = e.pos[n:]
}

func (e *constEnv) unset(args []word) {
	for _, w := range args {
		switch {
		case w.dynamic:
			e.off = true
			return
		case w.text == "-v":
		case strings.HasPrefix(w.text, "-"):
			// -f removes functions and -n follows namerefs.
			e.off = true
			return
		default:
			delete(e.vars, w.text)
		}
	}
}

// lookup returns the value of a tracked variable or positional parameter.
func (e *constEnv) lookup(name string) (string, bool) {
	if e == nil || e.off {
		return "", false
	}
	if n, err := strconv.Atoi(name); err == nil {
		if n < 1 || !e.posKnown {
			return "", false
		}
		if n > len(e.pos) {
			return "", true
		}
		return e.pos[n-1], true
	}
	v, ok := e.vars[name]
	return v, ok
}

// constParam resolves a plain $NAME, ${NAME}, $1 or ${10}. Anything else, and
// any name that is not tracked, is dynamic. Unquoted, a value that is empty,
// would split into several words or would glob is dynamic too, because one
// resolved word would not match what the shell builds.
func (wb *wordBuilder) constParam(p *syntax.ParamExp, inDouble bool) {
	name, ok := simpleParamName(p)
	if !ok {
		wb.out.dynamic = true
		return
	}
	v, found := wb.x.env.lookup(name)
	if !found || !inDouble && (v == "" || splitsOrGlobs(v)) {
		wb.out.dynamic = true
		return
	}
	wb.text.WriteString(v)
	wb.out.expanded = true
}

// splitsOrGlobs reports a value an unquoted expansion would split on the
// default IFS or expand as a pathname pattern.
func splitsOrGlobs(v string) bool {
	return strings.ContainsAny(v, " \t\n*?[")
}

func hasParam(w *syntax.Word) (found bool) {
	syntax.Walk(w, func(n syntax.Node) bool {
		if _, ok := n.(*syntax.ParamExp); ok {
			found = true
		}
		return !found
	})
	return found
}

// hasAssignExpansion reports an expansion that assigns a variable as a side
// effect: ${NAME=word}, ${NAME:=word}, or an arithmetic assignment such as
// $((IFS=53)) and $((x++)). Arithmetic assignments matter even when the result
// is unused: they can change IFS or any other variable the tracker later reads.
func hasAssignExpansion(n syntax.Node) (found bool) {
	syntax.Walk(n, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.ParamExp:
			if n.Exp != nil &&
				(n.Exp.Op == syntax.AssignUnset || n.Exp.Op == syntax.AssignUnsetOrNull) {
				found = true
			}
		case *syntax.BinaryArithm:
			switch n.Op {
			case syntax.Assgn, syntax.AddAssgn, syntax.SubAssgn, syntax.MulAssgn,
				syntax.QuoAssgn, syntax.RemAssgn, syntax.AndAssgn, syntax.OrAssgn,
				syntax.XorAssgn, syntax.ShlAssgn, syntax.ShrAssgn:
				found = true
			}
		case *syntax.UnaryArithm:
			if n.Op == syntax.Inc || n.Op == syntax.Dec {
				found = true
			}
		}
		return !found
	})
	return found
}
