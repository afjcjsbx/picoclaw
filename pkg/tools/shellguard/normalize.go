package shellguard

import "strings"

// NormalizeForDeny rewrites shell constructs whose value is fixed but whose
// written form can hide a command from the byte-level deny patterns:
// backslash-newline continuations, ANSI-C quoted strings and $IFS separators.
// It is deliberately not a shell evaluator and does not expand arbitrary
// variables.
func NormalizeForDeny(command string) string {
	command = stripShellLineContinuations(command)
	command = decodeANSICQuotes(command)
	command = strings.ReplaceAll(command, "${IFS}", " ")
	command = strings.ReplaceAll(command, "$IFS", " ")
	return command
}

// normalizeForParse applies only the fixed rewrites needed before parsing a
// command for canonical/risk analysis. Unlike NormalizeForDeny it leaves
// ANSI-C quoting in place: literalWord decodes every shell word exactly once,
// so pre-decoding here would make r\\m and $'r\m' look like rm.
func normalizeForParse(command string) string {
	command = stripShellLineContinuations(command)
	command = strings.ReplaceAll(command, "${IFS}", " ")
	command = strings.ReplaceAll(command, "$IFS", " ")
	return command
}

// stripShellLineContinuations removes backslash-newline pairs, which the shell
// joins before parsing. Both LF and CRLF are handled.
func stripShellLineContinuations(command string) string {
	if !strings.Contains(command, "\\\n") && !strings.Contains(command, "\\\r\n") {
		return command
	}

	var b strings.Builder
	b.Grow(len(command))
	for i := 0; i < len(command); i++ {
		if command[i] == '\\' {
			if i+1 < len(command) && command[i+1] == '\n' {
				i++
				continue
			}
			if i+2 < len(command) && command[i+1] == '\r' && command[i+2] == '\n' {
				i += 2
				continue
			}
		}
		b.WriteByte(command[i])
	}
	return b.String()
}

// decodeANSICQuotes replaces $'...' words with the bytes they denote. The
// scanner leaves $'...' inside single or double quotes alone because the shell
// does not treat it as ANSI-C quoting there.
func decodeANSICQuotes(command string) string {
	if !strings.Contains(command, "$'") {
		return command
	}

	var b strings.Builder
	b.Grow(len(command))
	inSingle, inDouble := false, false
	for i := 0; i < len(command); {
		switch {
		case inSingle:
			b.WriteByte(command[i])
			if command[i] == '\'' {
				inSingle = false
			}
			i++
		case inDouble:
			b.WriteByte(command[i])
			if command[i] == '\\' && i+1 < len(command) {
				b.WriteByte(command[i+1])
				i += 2
				continue
			}
			if command[i] == '"' {
				inDouble = false
			}
			i++
		case command[i] == '\'':
			inSingle = true
			b.WriteByte(command[i])
			i++
		case command[i] == '"':
			inDouble = true
			b.WriteByte(command[i])
			i++
		case command[i] == '\\' && i+1 < len(command):
			b.WriteByte(command[i])
			b.WriteByte(command[i+1])
			i += 2
		case command[i] == '$' && i+1 < len(command) && command[i+1] == '\'':
			end, decoded, ok := decodeANSICQuote(command[i+2:])
			if !ok {
				b.WriteByte(command[i])
				i++
				continue
			}
			b.WriteString(decoded)
			i += 2 + end
		default:
			b.WriteByte(command[i])
			i++
		}
	}
	return b.String()
}

// decodeANSICQuote decodes the body of a $'...' word starting after the
// opening quote. end is the number of bytes consumed, including the closing
// quote.
func decodeANSICQuote(body string) (end int, decoded string, ok bool) {
	var b strings.Builder
	b.Grow(len(body))
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\'':
			return i + 1, b.String(), true
		case '\\':
			if i+1 >= len(body) {
				return 0, "", false
			}
			i++
			switch body[i] {
			case 'a':
				b.WriteByte('\a')
			case 'b':
				b.WriteByte('\b')
			case 'e', 'E':
				b.WriteByte(0x1b)
			case 'f':
				b.WriteByte('\f')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'v':
				b.WriteByte('\v')
			case '\\':
				b.WriteByte('\\')
			case '\'':
				b.WriteByte('\'')
			case '"':
				b.WriteByte('"')
			case '?':
				b.WriteByte('?')
			case 'c':
				if i+1 < len(body) {
					i++
					if body[i] == '?' {
						b.WriteByte(0x7f)
					} else {
						b.WriteByte(body[i] & 0x1f)
					}
				} else {
					b.WriteString(`\c`)
				}
			case 'x':
				if value, n := parseANSIHex(body[i+1:], 2); n > 0 {
					b.WriteByte(byte(value))
					i += n
				} else {
					b.WriteString(`\x`)
				}
			case 'u':
				if value, n := parseANSIHex(body[i+1:], 4); n > 0 {
					b.WriteRune(rune(value))
					i += n
				} else {
					b.WriteString(`\u`)
				}
			case 'U':
				if value, n := parseANSIHex(body[i+1:], 8); n > 0 {
					b.WriteRune(rune(value))
					i += n
				} else {
					b.WriteString(`\U`)
				}
			case '0', '1', '2', '3', '4', '5', '6', '7':
				value := int(body[i] - '0')
				n := 1
				for n < 3 && i+n < len(body) && body[i+n] >= '0' && body[i+n] <= '7' {
					value = value*8 + int(body[i+n]-'0')
					n++
				}
				b.WriteByte(byte(value))
				i += n - 1
			default:
				b.WriteByte('\\')
				b.WriteByte(body[i])
			}
		default:
			b.WriteByte(body[i])
		}
	}
	return 0, "", false
}

func parseANSIHex(s string, maxDigits int) (value int, digits int) {
	for digits < maxDigits && digits < len(s) {
		d := hexValue(s[digits])
		if d < 0 {
			break
		}
		value = value*16 + d
		digits++
	}
	return value, digits
}

func hexValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	default:
		return -1
	}
}
