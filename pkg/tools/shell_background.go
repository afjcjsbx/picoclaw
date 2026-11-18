package tools

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
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
	file, err := parseShellCommand(command)
	if err != nil || len(file.Stmts) == 0 {
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
func untrackedDetachReason(command string, tracked, trailingConverted bool) string {
	file, err := parseShellCommand(command)
	if err != nil {
		return untrackedDetachFallback(command, tracked)
	}

	var converted *syntax.Stmt
	if trailingConverted && len(file.Stmts) > 0 {
		converted = file.Stmts[len(file.Stmts)-1]
	}

	reason := ""
	syntax.Walk(file, func(node syntax.Node) bool {
		if reason != "" {
			return false
		}
		if call, ok := node.(*syntax.CallExpr); ok && len(call.Args) > 0 {
			name := shellWordText(call.Args[0])
			if detachCommands[name] && !tracked {
				reason = "Command blocked: '" + name + "' detaches the process from the exec session. " +
					"Use background=true so PicoClaw can track it."
				return false
			}
		}
		if stmt, ok := node.(*syntax.Stmt); ok && stmt.Background && stmt != converted {
			reason = "Command blocked: '&' inside a command creates an untracked background process. " +
				"Run the whole command with background=true instead."
			return false
		}
		return true
	})
	return reason
}

// untrackedDetachFallback covers commands the parser cannot read. It only
// checks the first word, so it cannot hide an inner '&'; the shell will reject
// syntax it cannot parse in the same cases the parser does.
func untrackedDetachFallback(command string, tracked bool) string {
	fields := strings.Fields(command)
	if len(fields) == 0 || tracked {
		return ""
	}
	name := fields[0]
	if slash := strings.LastIndexByte(name, '/'); slash >= 0 {
		name = name[slash+1:]
	}
	if detachCommands[name] {
		return "Command blocked: '" + name + "' detaches the process from the exec session. " +
			"Use background=true so PicoClaw can track it."
	}
	return ""
}

func parseShellCommand(command string) (*syntax.File, error) {
	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	return parser.Parse(strings.NewReader(command), "")
}

func shellWordText(w *syntax.Word) string {
	if w == nil || len(w.Parts) != 1 {
		return ""
	}
	switch part := w.Parts[0].(type) {
	case *syntax.Lit:
		return part.Value
	case *syntax.SglQuoted:
		return part.Value
	case *syntax.DblQuoted:
		if len(part.Parts) == 1 {
			if lit, ok := part.Parts[0].(*syntax.Lit); ok {
				return lit.Value
			}
		}
	}
	return ""
}
