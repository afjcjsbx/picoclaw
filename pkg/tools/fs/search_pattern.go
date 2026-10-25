package fstools

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// searchPattern matches a candidate filename or path against the pattern
// syntax accepted by search_files. Patterns come in three forms:
//
//   - An empty pattern matches every file (listing mode).
//   - A pattern without glob metacharacters (*, ?, [, {) matches as a
//     substring of the slash-separated path relative to the search root, so
//     "docker-compose" finds "docker-compose.yml" and
//     "deploy/docker-compose.prod.yml".
//   - A pattern with glob metacharacters is matched as a glob. A glob that
//     contains "/" is matched against the whole relative path; one without
//     "/" is matched against the base name. "**" crosses directory
//     separators and "{a,b}" alternates, so "src/**/*.ts" and "*.{js,ts}"
//     behave like the ripgrep-style globs callers expect.
type searchPattern struct {
	matchAll  bool
	substring string
	glob      *regexp.Regexp
	baseOnly  bool
}

func newSearchPattern(pattern string) (searchPattern, error) {
	if pattern == "" {
		return searchPattern{matchAll: true}, nil
	}
	if !strings.ContainsAny(pattern, "*?[{") {
		return searchPattern{substring: filepath.ToSlash(pattern)}, nil
	}
	re, err := compileSearchGlob(pattern)
	if err != nil {
		return searchPattern{}, err
	}
	return searchPattern{glob: re, baseOnly: !strings.Contains(pattern, "/")}, nil
}

// match reports whether relPath, the path of a candidate relative to the
// search root, matches the pattern.
func (p searchPattern) match(relPath string) bool {
	relPath = filepath.ToSlash(relPath)
	switch {
	case p.matchAll:
		return true
	case p.substring != "":
		return strings.Contains(relPath, p.substring)
	case p.baseOnly:
		return p.glob.MatchString(path.Base(relPath))
	default:
		return p.glob.MatchString(relPath)
	}
}

// compileSearchGlob converts a glob into an anchored regular expression with
// ripgrep-style semantics: "*" and "?" do not cross "/", "**/" matches zero
// or more leading directory segments and a bare "**" matches anything,
// "{a,b}" alternates (nested braces allowed), and "[" classes are copied
// through.
func compileSearchGlob(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString(`\A`)
	if err := writeGlobRegexp(&b, glob); err != nil {
		return nil, err
	}
	b.WriteString(`\z`)
	return regexp.Compile(b.String())
}

func writeGlobRegexp(b *strings.Builder, glob string) error {
	for i := 0; i < len(glob); {
		switch c := glob[i]; c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				if i+2 < len(glob) && glob[i+2] == '/' {
					b.WriteString(`(?:[^/]+/)*`)
					i += 3
					continue
				}
				b.WriteString(`.*`)
				i += 2
				continue
			}
			b.WriteString(`[^/]*`)
			i++
		case '?':
			b.WriteString(`[^/]`)
			i++
		case '[':
			end := classEnd(glob, i+1)
			if end < 0 {
				b.WriteString(`\[`)
				i++
				continue
			}
			writeClass(b, glob[i+1:end])
			i = end + 1
		case '{':
			closeAt, alts, ok := braceAlternates(glob, i+1)
			if !ok {
				b.WriteString(`\{`)
				i++
				continue
			}
			b.WriteString(`(?:`)
			for altIndex, alt := range alts {
				if altIndex > 0 {
					b.WriteByte('|')
				}
				if err := writeGlobRegexp(b, alt); err != nil {
					return err
				}
			}
			b.WriteByte(')')
			i = closeAt + 1
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
			i++
		}
	}
	return nil
}

// classEnd returns the index of the "]" closing the character class that
// starts after the "[" at start-1, or -1 when the class is unterminated.
func classEnd(glob string, from int) int {
	if from < len(glob) && (glob[from] == '!' || glob[from] == '^') {
		from++
	}
	if from < len(glob) && glob[from] == ']' {
		// A "]" directly after the (possibly negated) opening is literal.
		from++
	}
	for i := from; i < len(glob); i++ {
		if glob[i] == ']' {
			return i
		}
	}
	return -1
}

func writeClass(b *strings.Builder, body string) {
	if body == "" {
		b.WriteString(`\[\]`)
		return
	}
	negated := false
	if body[0] == '!' || body[0] == '^' {
		negated = true
		body = body[1:]
	}
	b.WriteByte('[')
	if negated {
		b.WriteByte('^')
	}
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '\\' || c == ']' {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte(']')
}

// braceAlternates splits the "{...}" group that starts at from into its
// top-level alternatives and returns the index of the closing brace.
// ok is false when the group is unterminated.
func braceAlternates(glob string, from int) (int, []string, bool) {
	depth := 0
	start := from
	var alts []string
	for i := from; i < len(glob); i++ {
		switch glob[i] {
		case '{':
			depth++
		case '}':
			if depth == 0 {
				return i, append(alts, glob[start:i]), true
			}
			depth--
		case ',':
			if depth == 0 {
				alts = append(alts, glob[start:i])
				start = i + 1
			}
		}
	}
	return 0, nil, false
}
