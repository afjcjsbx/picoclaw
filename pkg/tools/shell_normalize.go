package tools

import "strings"

// normalizeForDeny resolves the fixed shell rewrites that can hide a command
// from the byte-level deny patterns. It is deliberately not a shell evaluator:
// it only joins backslash-newline continuations and replaces $IFS separators.
// The OS sandbox is the security boundary; this is a best-effort policy layer.
func normalizeForDeny(command string) string {
	command = stripLineContinuations(command)
	command = strings.ReplaceAll(command, "${IFS}", " ")
	command = strings.ReplaceAll(command, "$IFS", " ")
	return command
}

func stripLineContinuations(command string) string {
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
