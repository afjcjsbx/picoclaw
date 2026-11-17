package shellguard

import (
	"mvdan.cc/sh/v3/syntax"
)

// Analysis is the shared parse of one command. The exec guard uses it to serve
// heredoc stripping, canonical deny forms, workspace validation and risk
// classification without parsing the same text three or four times.
type Analysis struct {
	// File is the parsed command, or nil when the command does not parse.
	File *syntax.File
	// StrippedHeredoc is the written command with quoted heredoc bodies
	// replaced by a placeholder. It equals the input when nothing was stripped.
	StrippedHeredoc string
	// Canonical contains one space-joined form per simple command, including
	// nested shell scripts and wrapper commands.
	Canonical []string
	// CanonicalExact is false when the command contains fixed rewrites such as
	// $IFS or a backslash-newline continuation. Callers then fall back to
	// CanonicalCommands, which parses the normalized text.
	CanonicalExact bool
}

// Analyze parses command once and derives the forms the guard layers need.
// A parse error leaves File nil; the workspace and risk passes still reject
// unparseable input.
func Analyze(command string) Analysis {
	file, err := Parse(command)
	if err != nil {
		return Analysis{
			StrippedHeredoc: command,
			CanonicalExact:  normalizeForParse(command) == command,
		}
	}
	return Analysis{
		File:            file,
		StrippedHeredoc: stripQuotedHeredocBodiesFile(command, file),
		Canonical:       canonicalForms(file, 0),
		CanonicalExact:  normalizeForParse(command) == command,
	}
}
