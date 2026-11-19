package tools

import (
	"fmt"

	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

// exitCodeNotes explains non-zero statuses that are commonly normal. The map
// is deliberately small: an unknown exit code must stay a real error hint.
var exitCodeNotes = map[string]map[int]string{
	"grep":  {1: "grep exit 1 means no matches (not an error)"},
	"egrep": {1: "grep exit 1 means no matches (not an error)"},
	"fgrep": {1: "grep exit 1 means no matches (not an error)"},
	"rg":    {1: "ripgrep exit 1 means no matches (not an error)"},
	"ag":    {1: "exit 1 means no matches (not an error)"},
	"ack":   {1: "exit 1 means no matches (not an error)"},
	"diff":  {1: "diff exit 1 means files differ (not an error)"},
	"colordiff": {
		1: "diff exit 1 means files differ (not an error)",
	},
	"test": {1: "test exit 1 means the condition is false (not an error)"},
	"[":    {1: "test exit 1 means the condition is false (not an error)"},
	"git":  {1: "git exit 1 is often normal, for example git diff with changes"},
}

// signalExitNotes explains the shell's 128+signal statuses that models most
// often misread. The table is curated, not exhaustive: an unknown code stays a
// real error hint. 130 (SIGINT) is omitted because an interactive interrupt is
// usually expected.
var signalExitNotes = map[int]string{
	129: "SIGHUP (terminal closed or hangup)",
	132: "SIGILL (illegal instruction)",
	134: "SIGABRT (abort)",
	135: "SIGBUS (bus error)",
	136: "SIGFPE (arithmetic error)",
	137: "SIGKILL (often the OOM killer or kill -9)",
	139: "SIGSEGV (segmentation fault)",
	141: "SIGPIPE (wrote to a closed pipe)",
	143: "SIGTERM (terminated by kill or shutdown)",
}

// informativeExitNote returns a model-facing note for a non-zero status that
// has a well-known non-error meaning. The last canonical simple command is the
// one that supplies the shell's exit status.
func informativeExitNote(command string, analysis shellguard.Analysis, code int) string {
	if code <= 0 {
		return ""
	}
	if signal, ok := signalExitNotes[code]; ok {
		return fmt.Sprintf(
			"exit code %d usually means the command was terminated by %s", code, signal,
		)
	}
	forms := analysis.Forms(command)
	if len(forms) == 0 {
		return ""
	}
	name := formCommandName(forms[len(forms)-1])
	if name == "" {
		return ""
	}
	return exitCodeNotes[name][code]
}
