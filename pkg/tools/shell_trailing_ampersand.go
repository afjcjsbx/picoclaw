package tools

import "strings"

// shellLexState is the lexical state at the end of a scanned shell prefix.
type shellLexState uint8

const (
	shellLexNormal shellLexState = iota
	shellLexSingleQuote
	shellLexDoubleQuote
	shellLexComment
	shellLexHeredoc
	shellLexEscaped
)

// shellHeredoc is a pending heredoc redirection: the body starts after the
// newline that ends the current command line.
type shellHeredoc struct {
	delim     string
	stripTabs bool
}

// stripTrailingBackgroundAmpersand removes a final, unquoted '&' operator from
// a Unix shell command and reports whether it rewrote the command.
//
// The exec tool runs commands through `sh -c`. For `sh -c "server &"` the shell
// exits as soon as the job starts, so a foreground call would report success
// while the process keeps running untracked and its output pipe is closed.
// Callers convert these commands into tracked background sessions instead,
// where poll, wait, read, and kill can reach the process.
//
// The rewrite is deliberately conservative: only a standalone '&' at the very
// end of the command is recognized. Quotes, escapes, comments, heredoc bodies,
// `&&`, `|&`, and redirections are left untouched.
func stripTrailingBackgroundAmpersand(command string) (string, bool) {
	end := len(command)
	for end > 0 && isShellSpace(command[end-1]) {
		end--
	}
	if end == 0 || command[end-1] != '&' {
		return command, false
	}

	amp := end - 1
	if amp > 0 {
		switch command[amp-1] {
		case '&', '|', '>', '<':
			// &&, |&, >& and <& are not background operators.
			return command, false
		}
	}
	if shellPrefixState(command[:amp]) != shellLexNormal {
		return command, false
	}

	trimmed := strings.TrimRight(command[:amp], " \t")
	// A standalone '&' on its own line is a syntax error, not a background
	// operator: the newline already terminated the command.
	if trimmed == "" || trimmed[len(trimmed)-1] == '\n' {
		return command, false
	}

	return trimmed, true
}

// normalizeTrailingBackgroundAmpersand applies the Unix-only rewrite for the
// given OS. Keeping the platform gate in a helper makes the Windows no-op
// testable from any host.
func normalizeTrailingBackgroundAmpersand(command, goos string) (string, bool) {
	if goos == "windows" {
		return command, false
	}
	return stripTrailingBackgroundAmpersand(command)
}

// shellPrefixState scans a shell command prefix and returns the lexical state
// at its end. It understands single and double quotes, backslash escapes,
// comments, and heredoc bodies, so a trailing '&' is only treated as an
// operator when the shell would treat it as one.
func shellPrefixState(s string) shellLexState {
	var pending []shellHeredoc
	var heredoc *shellHeredoc

	for i := 0; i < len(s); i++ {
		if heredoc != nil {
			lineEnd := strings.IndexByte(s[i:], '\n')
			line := s[i:]
			next := len(s)
			if lineEnd >= 0 {
				line = s[i : i+lineEnd]
				next = i + lineEnd + 1
			}

			compare := line
			if heredoc.stripTabs {
				compare = strings.TrimLeft(compare, "\t")
			}
			if compare == heredoc.delim {
				heredoc = nil
				if len(pending) > 0 {
					nextHeredoc := pending[0]
					pending = pending[1:]
					heredoc = &nextHeredoc
				}
				i = next - 1
				continue
			}
			if lineEnd < 0 {
				return shellLexHeredoc
			}
			i = next - 1
			continue
		}

		switch c := s[i]; c {
		case '\\':
			if i+1 >= len(s) {
				return shellLexEscaped
			}
			i++ // skip the escaped byte
		case '\'':
			// $'...' (ANSI-C quoting) honors backslash escapes; plain '...'
			// does not.
			ansiC := i > 0 && s[i-1] == '$'
			j := i + 1
			for j < len(s) {
				if ansiC && s[j] == '\\' && j+1 < len(s) {
					j += 2
					continue
				}
				if s[j] == '\'' {
					break
				}
				j++
			}
			if j >= len(s) {
				return shellLexSingleQuote
			}
			i = j
		case '"':
			j := i + 1
			for j < len(s) {
				if s[j] == '\\' && j+1 < len(s) {
					j += 2
					continue
				}
				if s[j] == '"' {
					break
				}
				j++
			}
			if j >= len(s) {
				return shellLexDoubleQuote
			}
			i = j
		case '#':
			// '#' starts a comment only at the beginning of a word.
			if i == 0 || isShellCommentBoundary(s[i-1]) {
				nl := strings.IndexByte(s[i:], '\n')
				if nl < 0 {
					return shellLexComment
				}
				i += nl // the loop increment moves past the newline
			}
		case '\n':
			if len(pending) > 0 {
				nextHeredoc := pending[0]
				pending = pending[1:]
				heredoc = &nextHeredoc
			}
		case '<':
			if i+1 < len(s) && s[i+1] == '<' {
				if i+2 < len(s) && s[i+2] == '<' {
					// Here-string (<<<), not a heredoc.
					i += 2
					continue
				}
				delim, stripTabs, next, ok := parseHeredocDelimiter(s, i+2)
				i = next - 1
				if ok {
					pending = append(pending, shellHeredoc{delim: delim, stripTabs: stripTabs})
				}
			}
		}
	}

	if heredoc != nil {
		return shellLexHeredoc
	}
	return shellLexNormal
}

// parseHeredocDelimiter reads the word after `<<` or `<<-` and returns the
// literal delimiter used by the shell for body matching.
func parseHeredocDelimiter(s string, start int) (delim string, stripTabs bool, end int, ok bool) {
	i := start
	if i < len(s) && s[i] == '-' {
		stripTabs = true
		i++
	}
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}

	var word strings.Builder
	for i < len(s) {
		switch c := s[i]; c {
		case ' ', '\t', '\n', ';', '|', '&', '<', '>', '(', ')':
			return word.String(), stripTabs, i, word.Len() > 0
		case '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return "", false, len(s), false
			}
			word.WriteString(s[i+1 : i+1+j])
			i += j + 2
		case '"':
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' && j+1 < len(s) {
					j++
				}
				word.WriteByte(s[j])
				j++
			}
			if j >= len(s) {
				return "", false, len(s), false
			}
			i = j + 1
		case '\\':
			if i+1 < len(s) {
				word.WriteByte(s[i+1])
				i += 2
			} else {
				i++
			}
		default:
			word.WriteByte(c)
			i++
		}
	}
	return word.String(), stripTabs, i, word.Len() > 0
}

func isShellSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

func isShellCommentBoundary(c byte) bool {
	switch c {
	case ' ', '\t', '\n', ';', '|', '&', '(', ')', '<', '>':
		return true
	}
	return false
}
