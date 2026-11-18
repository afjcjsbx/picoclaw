package tools

import "strings"

// normalizeTrailingBackgroundAmpersand converts a final, unquoted background
// operator into a tracked background run for the given OS. Keeping the
// platform gate in a helper makes the Windows no-op testable from any host.
//
// The exec tool runs commands through `sh -c`. For `sh -c "server &"` the shell
// exits as soon as the job starts, so a foreground call would report success
// while the process keeps running untracked and its output pipe is closed.
func normalizeTrailingBackgroundAmpersand(command, goos string) (string, bool) {
	if goos == "windows" {
		return command, false
	}
	scan := scanShellCommand(command)
	if len(scan.background) == 0 {
		return command, false
	}
	offset := scan.background[len(scan.background)-1]
	if strings.TrimSpace(command[offset+1:]) != "" {
		return command, false
	}
	prefix := strings.TrimRight(command[:offset], " \t")
	if prefix == "" || strings.HasSuffix(prefix, "\n") || strings.HasSuffix(prefix, ";") {
		return command, false
	}
	trimmed := strings.TrimRight(prefix, " \t\r\n")
	if trimmed == "" {
		return command, false
	}
	return trimmed, true
}

// detachCommands start a process outside the shell's foreground wait. A
// foreground exec call would return while the process keeps running with no
// sessionId to poll, wait or kill.
var detachCommands = map[string]bool{
	"nohup":  true,
	"setsid": true,
	"disown": true,
}

// untrackedDetachReason returns a model-facing block message when a command
// starts background work the exec session cannot track. tracked is true for an
// explicit background=true run; nohup/setsid/disown are then allowed (the shell
// itself is tracked), while an inner '&' operator is always rejected because it
// detaches a job from the tracked shell.
func untrackedDetachReason(command string, tracked bool) string {
	scan := scanShellCommand(command)
	if !tracked && detachCommands[scan.firstWord] {
		return "Command blocked: '" + scan.firstWord + "' detaches the process from the exec session. " +
			"Use background=true so PicoClaw can track it."
	}
	if len(scan.background) > 0 {
		return "Command blocked: '&' inside a command creates an untracked background process. " +
			"Run the whole command with background=true instead."
	}
	return ""
}

// shellScan is the small amount of shell syntax the exec session tracker needs:
// the first command word and the offset of every unquoted background operator.
// It deliberately does not try to be a shell parser.
type shellScan struct {
	firstWord  string
	background []int
}

func scanShellCommand(command string) shellScan {
	command = maskHeredocBodies(command)
	var scan shellScan
	inSingle, inDouble, escaped := false, false, false
	wordStart := -1
	firstWordDone := false

	flushWord := func(end int) {
		if firstWordDone || wordStart < 0 {
			return
		}
		scan.firstWord = command[wordStart:end]
		firstWordDone = true
	}

	for i := 0; i < len(command); i++ {
		c := command[i]
		if escaped {
			escaped = false
			continue
		}
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			switch c {
			case '\\':
				escaped = true
			case '"':
				inDouble = false
			}
			continue
		}

		switch c {
		case '\\':
			escaped = true
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '#':
			if wordStart < 0 {
				flushWord(i)
				for i < len(command) && command[i] != '\n' {
					i++
				}
			}
		case ' ', '\t', '\r', '\n', ';', '|', '(', ')':
			flushWord(i)
			wordStart = -1
		case '&':
			if i > 0 && (command[i-1] == '>' || command[i-1] == '<' || command[i-1] == '|') {
				continue
			}
			if i+1 < len(command) && (command[i+1] == '&' || command[i+1] == '>') {
				i++
				continue
			}
			flushWord(i)
			scan.background = append(scan.background, i)
			wordStart = -1
		default:
			if wordStart < 0 {
				wordStart = i
			}
		}
	}
	flushWord(len(command))
	return scan
}

// maskHeredocBodies replaces heredoc body bytes with spaces while preserving
// length and newlines. The background tracker only needs shell control
// operators, not the body text, so masking avoids treating an "&" in a heredoc
// as an untracked background process.
func maskHeredocBodies(command string) string {
	lines := strings.SplitAfter(command, "\n")
	var b strings.Builder
	b.Grow(len(command))
	delimiter, stripTabs := "", false
	for _, line := range lines {
		body := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if delimiter != "" {
			check := body
			if stripTabs {
				check = strings.TrimLeft(check, "\t")
			}
			if check == delimiter {
				b.WriteString(line)
				delimiter = ""
				continue
			}
			b.WriteString(strings.Repeat(" ", len(body)))
			if strings.HasSuffix(line, "\n") {
				b.WriteByte('\n')
			}
			continue
		}
		if d, tabs, ok := heredocDelimiter(body); ok {
			delimiter, stripTabs = d, tabs
		}
		b.WriteString(line)
	}
	return b.String()
}

// heredocDelimiter returns the delimiter of the first unquoted heredoc
// operator on the line. It is a lexical helper, not a shell parser.
func heredocDelimiter(line string) (string, bool, bool) {
	inSingle, inDouble, escaped := false, false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if escaped {
			escaped = false
			continue
		}
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			switch c {
			case '\\':
				escaped = true
			case '"':
				inDouble = false
			}
			continue
		}
		switch c {
		case '\\':
			escaped = true
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '<':
			if i+1 >= len(line) || line[i+1] != '<' || (i+2 < len(line) && line[i+2] == '<') {
				continue
			}
			j := i + 2
			tabs := false
			if j < len(line) && line[j] == '-' {
				tabs = true
				j++
			}
			for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
				j++
			}
			if j >= len(line) {
				return "", false, false
			}
			var delimiter strings.Builder
			if line[j] == '\'' || line[j] == '"' {
				quote := line[j]
				j++
				for j < len(line) && line[j] != quote {
					delimiter.WriteByte(line[j])
					j++
				}
			} else {
				for j < len(line) && !strings.ContainsRune(" \t;|&()<>", rune(line[j])) {
					delimiter.WriteByte(line[j])
					j++
				}
			}
			if delimiter.Len() == 0 {
				return "", false, false
			}
			return delimiter.String(), tabs, true
		}
	}
	return "", false, false
}
