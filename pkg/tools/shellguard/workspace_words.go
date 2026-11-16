package shellguard

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

func (s *workspaceState) expandWord(w *syntax.Word, assignment bool) word {
	var out word
	if w == nil || len(w.Parts) == 0 {
		return out
	}

	if lit, ok := w.Parts[0].(*syntax.Lit); ok {
		if assignment {
			if prefix, rest, ok := assignmentTilde(lit.Value); ok {
				out.text = prefix
				out.homePath = true
				s.appendTilde(&out, rest)
				s.addParts(&out, w.Parts[1:])
				return out
			}
		}
		if strings.HasPrefix(lit.Value, "~") {
			out.homePath = true
			s.appendTilde(&out, lit.Value[1:])
			s.addParts(&out, w.Parts[1:])
			return out
		}
	}

	s.addParts(&out, w.Parts)
	return out
}

func (s *workspaceState) addParts(out *word, parts []syntax.WordPart) {
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
			out.text += p.Value
		case *syntax.SglQuoted:
			out.text += p.Value
			if p.Dollar {
				out.dynamic = true
			}
		case *syntax.DblQuoted:
			s.addParts(out, p.Parts)
		case *syntax.ParamExp:
			name, ok := simpleParamName(p)
			if !ok {
				// ${HOME:-...}, ${#HOME} and similar forms still start from
				// the home directory. Treat a leading one as an unresolvable
				// home path instead of letting the literal suffix through.
				if p.Param != nil && p.Param.Value == "HOME" && atPathStart(out.text) {
					out.homePath = true
				}
				out.dynamic = true
				continue
			}
			switch name {
			case "HOME":
				if atPathStart(out.text) {
					out.homePath = true
				}
				home, ok := s.effectiveHome()
				if !ok {
					out.dynamic = true
					continue
				}
				out.text += home
			case "PWD":
				if atPathStart(out.text) {
					out.homePath = true
				}
				cwd := s.currentCwd()
				if cwd == "" || s.pwdOverridden {
					out.dynamic = true
					continue
				}
				out.text += cwd
			case "OLDPWD":
				if atPathStart(out.text) {
					out.homePath = true
				}
				out.dynamic = true
			default:
				out.dynamic = true
			}
		default:
			out.dynamic = true
		}
	}
}

func (s *workspaceState) appendTilde(out *word, rest string) {
	home, ok := s.effectiveHome()
	if !ok {
		out.dynamic = true
		return
	}
	switch {
	case rest == "":
		out.text += home
	case strings.HasPrefix(rest, "/"):
		out.text += home + rest
	default:
		// ~user, ~+, ~- need the passwd database or shell state.
		out.dynamic = true
	}
}

// assignmentTilde splits an assignment word such as X=~/path into its literal
// prefix and the part after the tilde. Bash expands ~ after = in assignment
// context, including arguments to declare/export/local/readonly/typeset.
func assignmentTilde(value string) (prefix, rest string, ok bool) {
	eq := strings.IndexByte(value, '=')
	if eq <= 0 || !isAssignment(value[:eq+1]) {
		return "", "", false
	}
	after := value[eq+1:]
	if !strings.HasPrefix(after, "~") {
		return "", "", false
	}
	return value[:eq+1], after[1:], true
}

func atPathStart(text string) bool {
	return text == "" || strings.HasSuffix(text, "=")
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

// checkPathFlags validates directory-valued options such as tar -C, git -C,
// make -C and env --chdir. A bare `..` there reaches the parent without a
// slash, so the byte-level traversal regex misses it.
