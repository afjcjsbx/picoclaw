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
	syntaxCLike syntaxFlavor = iota
	syntaxPython
	syntaxTOML
)

// syntaxChecker is the cheap checker for one family of extensions. skipped is
// true when the checker bailed out before reaching a verdict.
type syntaxChecker func(content []byte) (problem string, skipped bool)

// syntaxCheck is the outcome of the best-effort check. Checked is false when
// the extension has no cheap checker. Skipped is true when the checker bailed
// out before reaching a verdict, for example because nesting passed the fixed
// stack. Problem is empty when the check passed; otherwise it is a short,
// line-oriented description suitable for the model. The write itself is never
// rejected because of this check.
type syntaxCheck struct {
	Checked bool
	Skipped bool
	Problem string
}

// quickSyntaxCheck runs a best-effort syntax check over content.
func quickSyntaxCheck(path string, content []byte) syntaxCheck {
	check := syntaxCheckerForPath(path)
	if check == nil {
		return syntaxCheck{}
	}
	problem, skipped := check(content)
	return syntaxCheck{Checked: true, Skipped: skipped, Problem: problem}
}

// syntaxCheckerForPath maps a file extension to the cheapest useful check.
// The list is intentionally conservative: extensions whose syntax needs a real
// parser (JavaScript regex literals, Rust lifetimes, Java text blocks, YAML
// indentation) are left unchecked so the tool never reports a false problem.
func syntaxCheckerForPath(path string) syntaxChecker {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return checkJSON
	case ".go", ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh":
		return checkCLike
	case ".py", ".pyi":
		return checkPython
	case ".toml":
		return checkTOML
	default:
		return nil
	}
}

func checkJSON(content []byte) (string, bool) {
	if json.Valid(content) {
		return "", false
	}
	return "invalid JSON", false
}

func checkCLike(content []byte) (string, bool) { return checkDelimiters(content, syntaxCLike) }

func checkPython(content []byte) (string, bool) { return checkDelimiters(content, syntaxPython) }

func checkTOML(content []byte) (string, bool) { return checkDelimiters(content, syntaxTOML) }

// checkDelimiters scans for unbalanced brackets, unterminated strings, and
// unterminated comments. It is O(n) time and O(1) memory: the bracket stack is
// a fixed array and nothing is allocated while scanning. The Python and TOML
// flavors understand # comments and triple-quoted strings. skipped is true
// when nesting exceeds the stack cap and the scan stopped without a verdict.
func checkDelimiters(content []byte, flavor syntaxFlavor) (problem string, skipped bool) {
	var stack [maxSyntaxDepth]byte
	var stackLines [maxSyntaxDepth]int32
	depth := 0
	line := 1
	hashComments := flavor == syntaxPython || flavor == syntaxTOML

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
					return fmt.Sprintf("unterminated block comment starting at line %d", startLine), false
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
		case '"':
			var problem string
			i, line, problem = skipQuoted(content, i, line, flavor)
			if problem != "" {
				return problem, false
			}
		case '\'':
			if flavor == syntaxCLike && isCDigitSeparator(content, i) {
				i++
				continue
			}
			var problem string
			i, line, problem = skipQuoted(content, i, line, flavor)
			if problem != "" {
				return problem, false
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
				return fmt.Sprintf("unterminated raw string starting at line %d", startLine), false
			}
		case '{', '[', '(':
			if depth >= maxSyntaxDepth {
				// Too deeply nested for the fixed stack: skip rather than
				// report a problem that may not exist.
				return "", true
			}
			stack[depth] = c
			stackLines[depth] = int32(line)
			depth++
			i++
		case '}', ']', ')':
			if depth == 0 {
				return fmt.Sprintf("unexpected %q at line %d", c, line), false
			}
			open := stack[depth-1]
			if !closesDelimiter(open, c) {
				return fmt.Sprintf("mismatched %q at line %d (opened %q)", c, line, open), false
			}
			depth--
			i++
		default:
			i++
		}
	}

	if depth > 0 {
		return fmt.Sprintf("unclosed %q opened at line %d", stack[depth-1], stackLines[depth-1]), false
	}
	return "", false
}

// skipQuoted advances past a quoted literal. Triple-quoted literals are only
// recognized in Python and TOML; C-like languages use single-line strings and
// Go raw strings are handled by the backtick branch. TOML single-quoted
// literals do not process escapes. Python raw strings keep the backslash in the
// data, but it still prevents the following quote from closing the literal, so
// raw strings are scanned like normal strings here.
func skipQuoted(content []byte, i, line int, flavor syntaxFlavor) (int, int, string) {
	quote := content[i]
	startLine := line
	tripleQuotes := flavor == syntaxPython || flavor == syntaxTOML
	literal := flavor == syntaxTOML && quote == '\''
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
					end := i + 3
					if flavor == syntaxTOML {
						// A multiline TOML string may end with one or two quotes
						// that belong to the content; the last three are the
						// delimiter.
						for extra := 0; extra < 2 && end < len(content) && content[end] == quote; extra++ {
							end++
						}
					}
					return end, line, ""
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

// isCDigitSeparator reports whether the quote at i is a C/C++ digit separator
// such as the one in 1'000 or 0xDEAD'BEEF, rather than a character literal.
// C++14 and C23 require the separator to sit between digits.
func isCDigitSeparator(content []byte, i int) bool {
	return i > 0 && i+1 < len(content) &&
		isHexDigit(content[i-1]) && isHexDigit(content[i+1])
}

func isHexDigit(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
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
