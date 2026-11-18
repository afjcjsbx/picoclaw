package shellguard

import "strings"

const (
	maxBraceAlternatives = 64
	maxBraceDepth        = 8
	maxBraceOutputBytes  = 16 * 1024
)

// BraceExpand rewrites shell brace groups into the words they denote. The
// expansion is deliberately conservative: alternatives become space-separated
// words, so `{rm,-rf,/}` becomes `rm -rf /` and the deny/risk layers can see
// the command that a POSIX shell would run. It stops at the first parse it
// cannot bound; callers then fall back to the written form.
//
// The shell expands braces before parsing. This is not a full shell evaluator:
// it handles comma lists outside quotes, respects nested groups, and gives up
// on ranges and very large expansions.
func BraceExpand(command string) (string, bool) {
	if !strings.Contains(command, "{") || !strings.Contains(command, ",") {
		return "", false
	}
	expanded, changed := expandBraces(command, 0)
	if !changed || expanded == command || len(expanded) > maxBraceOutputBytes {
		return "", false
	}
	return expanded, true
}

func expandBraces(text string, depth int) (string, bool) {
	if depth > maxBraceDepth || len(text) > maxBraceOutputBytes {
		return text, false
	}
	start, end, alternatives, ok := findBraceGroup(text)
	if !ok {
		return text, false
	}

	parts := make([]string, 0, len(alternatives))
	for _, alternative := range alternatives {
		expanded, _ := expandBraces(alternative, depth+1)
		parts = append(parts, expanded)
	}

	prefix := strings.TrimSpace(text[:start])
	suffix := strings.TrimSpace(text[end+1:])
	joined := strings.Join(parts, " ")
	out := strings.TrimSpace(strings.Join([]string{prefix, joined, suffix}, " "))
	expanded, _ := expandBraces(out, depth+1)
	return expanded, true
}

// findBraceGroup returns the first unquoted brace group that contains a
// top-level comma. A literal or range group is skipped so a later group can
// still expand.
func findBraceGroup(text string) (start, end int, alternatives []string, ok bool) {
	inSingle, inDouble, escaped := false, false, false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}
		switch {
		case inSingle:
			if ch == '\'' {
				inSingle = false
			}
			continue
		case inDouble:
			if ch == '"' {
				inDouble = false
			}
			continue
		case ch == '\'':
			inSingle = true
			continue
		case ch == '"':
			inDouble = true
			continue
		case ch != '{':
			continue
		}

		groupEnd, alts, found := scanBraceGroup(text, i)
		if !found || len(alts) < 2 || len(alts) > maxBraceAlternatives {
			continue
		}
		return i, groupEnd, alts, true
	}
	return 0, 0, nil, false
}

// scanBraceGroup matches a brace group starting at start and splits its body on
// top-level commas. Nested braces and quoted text are kept intact.
func scanBraceGroup(text string, start int) (int, []string, bool) {
	depth := 0
	inSingle, inDouble, escaped := false, false, false
	bodyStart := start + 1
	var alternatives []string
	last := bodyStart
	for i := start; i < len(text); i++ {
		ch := text[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}
		switch {
		case inSingle:
			if ch == '\'' {
				inSingle = false
			}
			continue
		case inDouble:
			if ch == '"' {
				inDouble = false
			}
			continue
		case ch == '\'':
			inSingle = true
			continue
		case ch == '"':
			inDouble = true
			continue
		}
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				alternatives = append(alternatives, text[last:i])
				return i, alternatives, true
			}
		case ',':
			if depth == 1 {
				alternatives = append(alternatives, text[last:i])
				last = i + 1
			}
		}
	}
	return 0, nil, false
}
