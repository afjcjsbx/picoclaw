package shellguard

import (
	"sort"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// StripQuotedHeredocBodies replaces the body of every heredoc with a quoted
// delimiter by a placeholder. A quoted delimiter disables shell expansion, so
// the body is literal data: deny patterns must not treat backticks or command
// names inside it as executable. Unquoted heredoc bodies are left untouched
// because the shell expands and executes their substitutions.
//
// The command is parsed so a <<'EOF' sequence inside quotes or a comment is not
// mistaken for a heredoc. On a parse error the command is returned unchanged;
// the exec guard rejects unparseable commands separately.
func StripQuotedHeredocBodies(command string) string {
	file, err := Parse(command)
	if err != nil {
		return command
	}

	type bodyRange struct {
		start int
		end   int
	}
	var bodies []bodyRange
	syntax.Walk(file, func(node syntax.Node) bool {
		redir, ok := node.(*syntax.Redirect)
		if !ok || redir.Hdoc == nil || redir.Word == nil {
			return true
		}
		if !quotedHeredocDelimiter(redir.Word) {
			return true
		}
		start := int(redir.Hdoc.Pos().Offset())
		end := int(redir.Hdoc.End().Offset())
		if start < 0 || end > len(command) || start >= end {
			return true
		}
		// Hdoc.End includes the closing delimiter. Keep that line so the
		// remaining text still has the same heredoc shape; replace only the
		// literal body before it.
		body := command[start:end]
		if newline := strings.LastIndexByte(body, '\n'); newline >= 0 {
			end = start + newline + 1
		} else {
			end = start
		}
		bodies = append(bodies, bodyRange{start: start, end: end})
		return true
	})
	if len(bodies) == 0 {
		return command
	}

	sort.Slice(bodies, func(i, j int) bool { return bodies[i].start > bodies[j].start })
	const placeholder = "[quoted heredoc omitted]\n"
	result := command
	for _, body := range bodies {
		result = result[:body.start] + placeholder + result[body.end:]
	}
	return result
}

// quotedHeredocDelimiter reports whether every part of the delimiter word is
// quoted. A dynamic delimiter is not proven literal, so it is not stripped.
func quotedHeredocDelimiter(word *syntax.Word) bool {
	if word == nil || len(word.Parts) == 0 {
		return false
	}
	for _, part := range word.Parts {
		switch p := part.(type) {
		case *syntax.SglQuoted:
			continue
		case *syntax.DblQuoted:
			continue
		case *syntax.Lit:
			// A backslash in the literal delimiter is shell quoting
			// (<<\EOF). Without one, it is an unquoted delimiter.
			if strings.ContainsRune(p.Value, '\\') {
				continue
			}
			return false
		default:
			return false
		}
	}
	return true
}
