package tools

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"

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
// where poll, wait, read and kill can reach the process. The caller supplies
// the shared parse of the written command, so this helper does not parse again.
func normalizeTrailingBackgroundAmpersand(command string, analysis shellguard.Analysis, goos string) (string, bool) {
	if goos == "windows" {
		return command, false
	}
	file := analysis.File
	if file == nil || len(file.Stmts) == 0 {
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
// detaches a job from the tracked shell. trailingConverted is true when the
// caller rewrote the command's final '&' into a tracked background run.
func untrackedDetachReason(command string, analysis shellguard.Analysis, tracked, trailingConverted bool) string {
	forms := analysis.Canonical
	if !analysis.CanonicalExact {
		forms = shellguard.CanonicalCommands(command)
	}
	for _, form := range forms {
		fields := strings.Fields(form)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		if slash := strings.LastIndexByte(name, '/'); slash >= 0 {
			name = name[slash+1:]
		}
		if detachCommands[name] && !tracked {
			return "Command blocked: '" + name + "' detaches the process from the exec session. " +
				"Use background=true so PicoClaw can track it."
		}
	}

	if analysis.File == nil {
		return ""
	}
	var converted *syntax.Stmt
	if trailingConverted && len(analysis.File.Stmts) > 0 {
		converted = analysis.File.Stmts[len(analysis.File.Stmts)-1]
	}
	found := false
	syntax.Walk(analysis.File, func(node syntax.Node) bool {
		if found {
			return false
		}
		if stmt, ok := node.(*syntax.Stmt); ok && stmt.Background {
			if stmt == converted {
				return true
			}
			found = true
			return false
		}
		return true
	})
	if found {
		return "Command blocked: '&' inside a command creates an untracked background process. " +
			"Run the whole command with background=true instead."
	}
	return ""
}
