package shellguard

import "strings"

// unescapeUnquotedLit applies one level of POSIX unquoted backslash removal.
// A backslash-newline pair is removed entirely; any other backslash is removed
// and the escaped byte is kept. Two backslashes therefore become one literal
// backslash, not two levels of decoding.
func unescapeUnquotedLit(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		switch {
		case s[i+1] == '\n':
			i++
		case s[i+1] == '\r' && i+2 < len(s) && s[i+2] == '\n':
			i += 2
		default:
			i++
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// unescapeDoubleQuotedLit applies the narrower backslash rules used inside
// double quotes. Only $, `, ", \ and newline lose their backslash; before any
// other byte the backslash is literal.
func unescapeDoubleQuotedLit(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		switch s[i+1] {
		case '\n':
			i++
		case '\r':
			if i+2 < len(s) && s[i+2] == '\n' {
				i += 2
				continue
			}
			b.WriteByte(s[i])
		case '$', '`', '"', '\\':
			i++
			b.WriteByte(s[i])
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// decodeANSIValue decodes the body of a $'...' word. An unknown escape keeps
// its backslash, matching the shell. It returns false for an incomplete body.
func decodeANSIValue(value string) (string, bool) {
	end, decoded, ok := decodeANSICQuote(value + "'")
	if !ok || end != len(value)+1 {
		return "", false
	}
	return decoded, true
}
