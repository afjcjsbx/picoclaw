package shellguard

import "mvdan.cc/sh/v3/syntax"

// Constructs the guard cannot look inside. Each one runs code that no later
// pass sees: a command substitution is evaluated before its command is known,
// and a shell reading stdin runs whatever the previous command or heredoc
// supplies.
const (
	ConstructCommandSubstitution = "command substitution"
	ConstructPipeToShell         = "pipe to shell"
	ConstructShellHeredoc        = "shell heredoc"
)

// opaqueConstructs lists the opaque constructs in file, including those inside
// literal scripts it runs (sh -c, eval, trap, wrappers). Each construct is
// reported once.
func opaqueConstructs(file *syntax.File, depth int) []string {
	var found []string
	add := func(construct string) {
		for _, existing := range found {
			if existing == construct {
				return
			}
		}
		found = append(found, construct)
	}

	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CmdSubst:
			add(ConstructCommandSubstitution)
		case *syntax.BinaryCmd:
			if (n.Op == syntax.Pipe || n.Op == syntax.PipeAll) && stmtReadsShellStdin(n.Y) {
				add(ConstructPipeToShell)
			}
		case *syntax.Stmt:
			if hasHeredoc(n.Redirs) && stmtReadsShellStdin(n) {
				add(ConstructShellHeredoc)
			}
		case *syntax.CallExpr:
			for _, construct := range nestedConstructs(n, depth) {
				add(construct)
			}
		}
		return true
	})
	return found
}

// nestedConstructs reports the constructs inside the literal scripts a call
// runs. A script that does not parse is left to the other passes.
func nestedConstructs(call *syntax.CallExpr, depth int) []string {
	if len(call.Args) == 0 || !canNest(depth) {
		return nil
	}
	words := make([]word, len(call.Args))
	for i, arg := range call.Args {
		words[i] = literalWord(arg)
	}

	var found []string
	for _, script := range resolveLaunch(words).scripts {
		if script.dynamic {
			continue
		}
		file, err := Parse(normalizeForParse(script.text))
		if err != nil {
			continue
		}
		found = append(found, opaqueConstructs(file, depth+1)...)
	}
	return found
}

func hasHeredoc(redirs []*syntax.Redirect) bool {
	for _, redir := range redirs {
		switch redir.Op {
		case syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
			return true
		}
	}
	return false
}

func stmtReadsShellStdin(stmt *syntax.Stmt) bool {
	if stmt == nil {
		return false
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 {
		return false
	}
	words := make([]word, len(call.Args))
	for i, arg := range call.Args {
		words[i] = literalWord(arg)
	}
	return readsShellStdin(words, 0)
}

// readsShellStdin reports whether words run a shell that executes its stdin,
// directly or behind a wrapper such as env or timeout.
func readsShellStdin(words []word, depth int) bool {
	l := resolveLaunch(words)
	if l.bareShell && l.readsStdin {
		return true
	}
	if !canNest(depth) {
		return false
	}
	for _, argv := range l.argvs {
		if readsShellStdin(argv, depth+1) {
			return true
		}
	}
	return false
}
