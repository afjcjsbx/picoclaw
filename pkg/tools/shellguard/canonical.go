package shellguard

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// CanonicalCommands returns one canonical form per simple command in command.
// The parser removes quote boundaries and resolves concatenation, so a
// quote-spliced name such as r”m -rf / becomes "rm -rf /". Words that contain
// expansions contribute their literal prefix; a command with a dynamic name is
// skipped because its argv cannot be represented statically.
//
// Literal sh -c scripts and wrapper commands are inspected recursively, so the
// returned forms include the commands they would execute. The forms are
// intentionally a flat, space-joined view of argv (plus file redirection
// targets) for operator regex deny rules. They are not executed and do not
// replace the shell's own parsing.
func CanonicalCommands(command string) []string {
	return canonicalCommands(normalizeForParse(command), 0)
}

func canonicalCommands(command string, depth int) []string {
	if depth > maxShellNesting {
		return nil
	}

	file, err := Parse(command)
	if err != nil {
		return nil
	}
	return canonicalForms(file, depth)
}

// canonicalForms walks an already parsed file. Analyze reuses this with the
// same syntax.File that the workspace and risk passes consume.
func canonicalForms(file *syntax.File, depth int) []string {
	if file == nil || depth > maxShellNesting {
		return nil
	}

	var forms []string
	syntax.Walk(file, func(node syntax.Node) bool {
		stmt, ok := node.(*syntax.Stmt)
		if !ok {
			return true
		}
		call, ok := stmt.Cmd.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}

		words := make([]word, 0, len(call.Args))
		for _, arg := range call.Args {
			words = append(words, literalWord(arg))
		}
		forms = appendCanonicalCall(forms, words, stmt.Redirs, depth)
		return true
	})
	return forms
}

func appendCanonicalCall(forms []string, words []word, redirs []*syntax.Redirect, depth int) []string {
	if len(words) == 0 || words[0].dynamic || words[0].text == "" {
		// The command name is dynamic; its argv cannot be matched reliably.
		return forms
	}

	formWords := make([]string, 0, len(words)+len(redirs))
	for _, w := range words {
		if w.text != "" {
			formWords = append(formWords, w.text)
		}
	}
	formWords = appendRedirectWords(formWords, redirs)
	if len(formWords) > 0 {
		forms = append(forms, strings.Join(formWords, " "))
	}

	if !canNest(depth) {
		return forms
	}

	l := resolveLaunch(words)
	for _, script := range l.scripts {
		if !script.dynamic {
			forms = append(forms, canonicalCommands(normalizeForParse(script.text), depth+1)...)
		}
	}
	for _, argv := range l.argvs {
		forms = appendCanonicalCall(forms, argv, nil, depth+1)
	}
	return forms
}

func appendRedirectWords(words []string, redirs []*syntax.Redirect) []string {
	for _, redir := range redirs {
		if redir == nil || redir.Word == nil {
			continue
		}
		switch redir.Op {
		case syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
			// Heredoc bodies and here-strings are data, not argv. Quoted
			// heredoc bodies must not become a command form.
			continue
		}
		w := literalWord(redir.Word)
		if w.text == "" {
			continue
		}
		words = append(words, redir.Op.String()+w.text)
	}
	return words
}
