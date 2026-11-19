package shellguard

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// word is a shell word reduced to its literal parts. dynamic reports that the
// word contains an expansion, substitution or glob, which the static guard
// cannot resolve. homePath marks a word that starts with a tilde or $HOME.
type word struct {
	text     string
	dynamic  bool
	homePath bool
}

// wordExpander supplies the path expansions the workspace guard follows: ~,
// $HOME, $PWD. An empty value is unresolvable and makes the word dynamic. A nil
// expander treats every expansion as dynamic, which is what risk
// classification and canonical forms need.
type wordExpander struct {
	home string
	cwd  string
}

// literalWord reduces a word to its literal text without resolving any
// expansion.
func literalWord(w *syntax.Word) word {
	return buildWord(w, nil, false)
}

// buildWord is the only place a syntax.Word becomes a literal argv word, so
// every guard layer agrees on quoting, backslash removal and ANSI-C decoding.
// assignment enables the tilde expansion bash performs after = in assignment
// context.
func buildWord(w *syntax.Word, x *wordExpander, assignment bool) word {
	if w == nil || len(w.Parts) == 0 {
		return word{}
	}

	// A single plain or single-quoted part is already the final argv word, so
	// the direct path avoids the builder for the common case.
	if x == nil && len(w.Parts) == 1 {
		switch p := w.Parts[0].(type) {
		case *syntax.Lit:
			return word{text: unescapeUnquotedLit(p.Value)}
		case *syntax.SglQuoted:
			if !p.Dollar {
				return word{text: p.Value}
			}
			if decoded, ok := decodeANSIValue(p.Value); ok {
				return word{text: decoded}
			}
		}
	}

	wb := wordBuilder{x: x}
	parts := w.Parts
	if x != nil {
		if lit, ok := parts[0].(*syntax.Lit); ok {
			if prefix, rest, ok := tildePrefix(lit.Value, assignment); ok {
				wb.text.WriteString(prefix)
				wb.out.homePath = true
				wb.tilde(rest)
				parts = parts[1:]
			}
		}
	}
	wb.add(parts, false)
	wb.out.text = wb.text.String()
	return wb.out
}

// heredocScriptWord returns the text a heredoc or here-string feeds to a shell
// as its script. A quoted delimiter makes the body literal, so backslashes
// survive; an unquoted one expands like double quotes.
func heredocScriptWord(redir *syntax.Redirect, x *wordExpander) (word, bool) {
	switch redir.Op {
	case syntax.WordHdoc:
		// <<< here-strings keep their data in Word instead of Hdoc.
		if redir.Word == nil {
			return word{}, false
		}
		return buildWord(redir.Word, x, false), true
	case syntax.Hdoc, syntax.DashHdoc:
		if redir.Hdoc == nil {
			return word{}, false
		}
		if !quotedHeredocDelimiter(redir.Word) {
			wb := wordBuilder{x: x}
			wb.add(redir.Hdoc.Parts, true)
			wb.out.text = wb.text.String()
			return wb.out, true
		}
		var body word
		for _, part := range redir.Hdoc.Parts {
			lit, ok := part.(*syntax.Lit)
			if !ok {
				body.dynamic = true
				continue
			}
			body.text += lit.Value
		}
		return body, true
	}
	return word{}, false
}

type wordBuilder struct {
	x    *wordExpander
	out  word
	text strings.Builder
}

func (wb *wordBuilder) add(parts []syntax.WordPart, inDouble bool) {
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
			if inDouble {
				wb.text.WriteString(unescapeDoubleQuotedLit(p.Value))
			} else {
				wb.text.WriteString(unescapeUnquotedLit(p.Value))
			}
		case *syntax.SglQuoted:
			// Single quotes preserve backslashes. $'...' is an ANSI-C quoted
			// string, so decode its escapes to the bytes the shell would pass;
			// an unknown escape keeps its backslash.
			if !p.Dollar {
				wb.text.WriteString(p.Value)
			} else if decoded, ok := decodeANSIValue(p.Value); ok {
				wb.text.WriteString(decoded)
			} else {
				wb.text.WriteString(p.Value)
				wb.out.dynamic = true
			}
		case *syntax.DblQuoted:
			wb.add(p.Parts, true)
		case *syntax.ParamExp:
			wb.param(p)
		default:
			wb.out.dynamic = true
		}
	}
}

// param resolves the few parameters the workspace guard tracks. Every other
// expansion, and every expansion without an expander, is dynamic.
func (wb *wordBuilder) param(p *syntax.ParamExp) {
	if wb.x == nil {
		wb.out.dynamic = true
		return
	}

	name, ok := simpleParamName(p)
	if !ok {
		// ${HOME:-...}, ${#HOME} and similar forms still start from the home
		// directory. Treat a leading one as an unresolvable home path instead
		// of letting the literal suffix through.
		if p.Param != nil && p.Param.Value == "HOME" && wb.atPathStart() {
			wb.out.homePath = true
		}
		wb.out.dynamic = true
		return
	}

	var value string
	switch name {
	case "HOME":
		value = wb.x.home
	case "PWD":
		value = wb.x.cwd
	case "OLDPWD":
		// The previous directory is shell state the guard does not track.
	default:
		wb.out.dynamic = true
		return
	}
	if wb.atPathStart() {
		wb.out.homePath = true
	}
	if value == "" {
		wb.out.dynamic = true
		return
	}
	wb.text.WriteString(value)
}

// tilde appends the expansion of ~ followed by rest.
func (wb *wordBuilder) tilde(rest string) {
	switch {
	case wb.x.home == "":
		wb.out.dynamic = true
	case rest == "":
		wb.text.WriteString(wb.x.home)
	case strings.HasPrefix(rest, "/"):
		wb.text.WriteString(wb.x.home + unescapeUnquotedLit(rest))
	default:
		// ~user, ~+, ~- need the passwd database or shell state.
		wb.out.dynamic = true
	}
}

func (wb *wordBuilder) atPathStart() bool {
	text := wb.text.String()
	return text == "" || strings.HasSuffix(text, "=")
}

// tildePrefix splits a leading literal into the text kept as is and the part
// after the tilde that expands to the home directory. In assignment context
// bash also expands ~ after =, as in X=~/path or export X=~/path.
func tildePrefix(lit string, assignment bool) (prefix, rest string, ok bool) {
	if assignment {
		if eq := strings.IndexByte(lit, '='); eq > 0 && isAssignment(lit[:eq+1]) &&
			strings.HasPrefix(lit[eq+1:], "~") {
			return lit[:eq+1], lit[eq+2:], true
		}
	}
	if strings.HasPrefix(lit, "~") {
		return "", lit[1:], true
	}
	return "", "", false
}

func simpleParamName(p *syntax.ParamExp) (string, bool) {
	if p.Param == nil || p.Flags != nil || p.Excl || p.Length || p.Width || p.IsSet ||
		p.Split != syntax.OptUnset || p.GlobSubst != syntax.OptUnset ||
		p.RcExpand != syntax.OptUnset || p.NestedParam != nil || p.Index != nil ||
		len(p.Modifiers) != 0 || p.Slice != nil || p.Repl != nil ||
		p.Names != 0 || p.Exp != nil {
		return "", false
	}
	return p.Param.Value, true
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
