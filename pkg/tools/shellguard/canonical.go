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
	return canonicalCommands(NormalizeForDeny(command), 0)
}

func canonicalCommands(command string, depth int) []string {
	if depth > maxShellNesting {
		return nil
	}

	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
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
	if len(words) == 0 || !words[0].literal || words[0].text == "" {
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

	if depth >= maxShellNesting {
		return forms
	}

	name := baseCommand(words[0].text)
	if isShellCommand(name) {
		if script, ok := shellScriptArg(words[1:]); ok && script.literal {
			forms = append(forms, canonicalCommands(NormalizeForDeny(script.text), depth+1)...)
		}
		return forms
	}
	switch name {
	case "eval":
		if script, ok := joinLiteralWords(words[1:]); ok && script != "" {
			forms = append(forms, canonicalCommands(NormalizeForDeny(script), depth+1)...)
		}
		return forms
	case "trap":
		for _, w := range words[1:] {
			if !w.literal {
				return forms
			}
			switch w.text {
			case "-l", "-p":
				return forms
			case "--":
				continue
			case "", "-":
				return forms
			}
			if strings.HasPrefix(w.text, "-") {
				continue
			}
			forms = append(forms, canonicalCommands(NormalizeForDeny(w.text), depth+1)...)
			return forms
		}
		return forms
	}
	if spec, ok := wrapperSpecs[name]; ok {
		nested, found, dynamic := wrapperNested(words[1:], spec)
		if found && !dynamic && len(nested) > 0 {
			forms = appendCanonicalCall(forms, nested, nil, depth+1)
		}
	}
	return forms
}

func joinLiteralWords(words []word) (string, bool) {
	texts := make([]string, 0, len(words))
	for _, w := range words {
		if !w.literal {
			return "", false
		}
		texts = append(texts, w.text)
	}
	return strings.Join(texts, " "), true
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
