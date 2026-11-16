package tools

import (
	"strings"

	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

// normalizeTrailingBackgroundAmpersand converts a final, unquoted background
// operator into a tracked background run for the given OS. Keeping the
// platform gate in a helper makes the Windows no-op testable from any host.
//
// The exec tool runs commands through `sh -c`. For `sh -c "server &"` the shell
// exits as soon as the job starts, so a foreground call would report success
// while the process keeps running untracked and its output pipe is closed.
// Callers convert these commands into tracked background sessions instead,
// where poll, wait, read and kill can reach the process.
func normalizeTrailingBackgroundAmpersand(command, goos string) (string, bool) {
	if goos == "windows" {
		return command, false
	}
	file, err := shellguard.Parse(command)
	if err != nil || file == nil || len(file.Stmts) == 0 {
		return command, false
	}

	last := file.Stmts[len(file.Stmts)-1]
	if !last.Background || !last.Semicolon.IsValid() {
		return command, false
	}
	offset := int(last.Semicolon.Offset())
	if offset < 0 || offset >= len(command) || command[offset] != '&' {
		return command, false
	}

	// Only a standalone operator at the end is rewritten. A comment after the
	// operator keeps the old foreground behavior, matching the documented
	// rewrite rules.
	remainder := command[offset+1:]
	if strings.TrimSpace(remainder) != "" {
		return command, false
	}
	trimmed := strings.TrimRight(command[:offset]+remainder, " \t\r\n")
	if trimmed == "" {
		return command, false
	}
	return trimmed, true
}
