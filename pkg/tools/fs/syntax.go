package fstools

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// maxSyntaxDepth bounds the delimiter stack of the quick syntax check. The
// stack is a fixed-size array, so a deeply nested file cannot grow the
// checker's memory; past the cap the check is skipped instead of guessing.
const maxSyntaxDepth = 256

// syntaxFlavor selects the comment and string rules of a file extension.
type syntaxFlavor uint8

const (
	syntaxNone syntaxFlavor = iota
	syntaxJSON
	syntaxCLike
	syntaxPython
	syntaxTOML
)

// syntaxFlavorForPath maps a file extension to the cheapest useful check.
// The list is intentionally conservative: extensions whose syntax needs a real
// parser (JavaScript regex literals, Rust lifetimes, Java text blocks, YAML
// indentation) are left unchecked so the tool never reports a false problem.
func syntaxFlavorForPath(path string) syntaxFlavor {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return syntaxJSON
	case ".go", ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh":
		return syntaxCLike
	case ".py", ".pyi":
		return syntaxPython
	case ".toml":
		return syntaxTOML
	default:
		return syntaxNone
	}
}

// quickSyntaxCheck runs a best-effort syntax check over content. checked is
// false when the extension has no cheap checker. problem is empty when the
// check passed; otherwise it is a short, line-oriented description suitable
// for the model. The write itself is never rejected because of this check.
func quickSyntaxCheck(path string, content []byte) (checked bool, problem string) {
	switch syntaxFlavorForPath(path) {
	case syntaxJSON:
		if json.Valid(content) {
			return true, ""
		}
		return true, "invalid JSON"
	case syntaxCLike:
		return true, checkDelimiters(content, syntaxCLike)
	case syntaxPython:
		return true, checkDelimiters(content, syntaxPython)
	case syntaxTOML:
		return true, checkDelimiters(content, syntaxTOML)
	default:
		return false, ""
	}
}

// checkDelimiters scans for unbalanced brackets, unterminated strings, and
// unterminated comments. It is O(n) time and O(1) memory: the bracket stack is
// a fixed array and nothing is allocated while scanning. The Python and TOML
// flavors understand # comments and triple-quoted strings.
func checkDelimiters(content []byte, flavor syntaxFlavor) string {
	var stack [maxSyntaxDepth]byte
	var stackLines [maxSyntaxDepth]int32
	depth := 0
	line := 1
	hashComments := flavor == syntaxPython || flavor == syntaxTOML
	tripleQuotes := hashComments

	for i := 0; i < len(content); {
		c := content[i]
		switch c {
		case '\n':
			line++
			i++
		case '/':
			if hashComments || i+1 >= len(content) {
				i++
				continue
			}
			switch content[i+1] {
			case '/':
				i += 2
				for i < len(content) && content[i] != '\n' {
					i++
				}
			case '*':
				startLine := line
				i += 2
				closed := false
				for i < len(content) {
					if content[i] == '\n' {
						line++
					}
					if content[i] == '*' && i+1 < len(content) && content[i+1] == '/' {
						i += 2
						closed = true
						break
					}
					i++
				}
				if !closed {
					return fmt.Sprintf("unterminated block comment starting at line %d", startLine)
				}
			default:
				i++
			}
		case '#':
			if !hashComments {
				i++
				continue
			}
			i++
			for i < len(content) && content[i] != '\n' {
				i++
			}
		case '"', '\'':
			// TOML single-quoted strings are literal; Python raw strings keep
			// backslashes as data. Both must not treat a backslash as an escape
			// or a valid string can look unterminated.
			literal := (flavor == syntaxTOML && c == '\'') ||
				(flavor == syntaxPython && pythonRawPrefix(content, i))
			var problem string
			i, line, problem = skipQuoted(content, i, line, tripleQuotes, literal)
			if problem != "" {
				return problem
			}
		case '`':
			if hashComments {
				i++
				continue
			}
			startLine := line
			i++
			closed := false
			for i < len(content) {
				if content[i] == '\n' {
					line++
				}
				if content[i] == '`' {
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return fmt.Sprintf("unterminated raw string starting at line %d", startLine)
			}
		case '{', '[', '(':
			if depth >= maxSyntaxDepth {
				// Too deeply nested for the fixed stack: skip rather than
				// report a problem that may not exist.
				return ""
			}
			stack[depth] = c
			stackLines[depth] = int32(line)
			depth++
			i++
		case '}', ']', ')':
			if depth == 0 {
				return fmt.Sprintf("unexpected %q at line %d", c, line)
			}
			open := stack[depth-1]
			if !closesDelimiter(open, c) {
				return fmt.Sprintf("mismatched %q at line %d (opened %q)", c, line, open)
			}
			depth--
			i++
		default:
			i++
		}
	}

	if depth > 0 {
		return fmt.Sprintf("unclosed %q opened at line %d", stack[depth-1], stackLines[depth-1])
	}
	return ""
}

// skipQuoted advances past a quoted literal. Triple-quoted literals are only
// recognized in Python and TOML; C-like languages use single-line strings and
// Go raw strings are handled by the backtick branch. literal disables escape
// processing for TOML literal strings and Python raw strings.
func skipQuoted(content []byte, i, line int, tripleQuotes, literal bool) (int, int, string) {
	quote := content[i]
	startLine := line
	triple := tripleQuotes && i+2 < len(content) && content[i+1] == quote && content[i+2] == quote
	if triple {
		i += 3
	} else {
		i++
	}

	for i < len(content) {
		c := content[i]
		if c == '\\' && !literal {
			// Skip the escaped byte. A backslash-newline continues the literal
			// in most languages, so the newline must not be lost.
			if i+1 < len(content) && content[i+1] == '\n' {
				line++
			}
			i += 2
			continue
		}
		if c == '\n' {
			if !triple {
				break
			}
			line++
		}
		if c == quote {
			if triple {
				if i+2 < len(content) && content[i+1] == quote && content[i+2] == quote {
					return i + 3, line, ""
				}
				i++
				continue
			}
			return i + 1, line, ""
		}
		i++
	}

	return i, line, fmt.Sprintf("unterminated string starting at line %d", startLine)
}

// pythonRawPrefix reports whether the string starting at quoteAt has an r/R
// prefix (r"", rb"", fr"", ...), which makes backslashes literal.
func pythonRawPrefix(content []byte, quoteAt int) bool {
	for i := quoteAt - 1; i >= 0; i-- {
		switch content[i] {
		case 'r', 'R':
			return true
		case 'b', 'B', 'u', 'U', 'f', 'F':
			continue
		default:
			return false
		}
	}
	return false
}

func closesDelimiter(open, closer byte) bool {
	switch open {
	case '{':
		return closer == '}'
	case '[':
		return closer == ']'
	case '(':
		return closer == ')'
	default:
		return false
	}
}
