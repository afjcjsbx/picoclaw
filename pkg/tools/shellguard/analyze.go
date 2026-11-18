package shellguard

import (
	"mvdan.cc/sh/v3/syntax"
)

// Analysis is the shared parse of one command. The exec guard uses it to serve
// heredoc stripping, canonical deny forms, workspace validation and risk
// classification without parsing the same text three or four times.
type Analysis struct {
	// File is the written command parsed as-is, or nil when it does not parse.
	// Workspace validation uses it so paths and offsets match the source text.
	File *syntax.File
	// RiskFile is the parse used for risk classification. It equals File unless
	// fixed rewrites such as $IFS or a backslash-newline continuation changed
	// word boundaries; then it is the normalized parse, so an obfuscated
	// command cannot become an allowed unknown.
	RiskFile *syntax.File
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
// A parse error leaves File and RiskFile nil; the workspace pass rejects
// unparseable input, while the risk pass marks it unknown (allowed by default,
// reviewable by smart approvals).
func Analyze(command string) Analysis {
	normalized := normalizeForParse(command)
	file, err := Parse(command)
	if err != nil {
		return Analysis{
			StrippedHeredoc: command,
			CanonicalExact:  normalized == command,
		}
	}
	riskFile := file
	if normalized != command {
		if normalizedFile, parseErr := Parse(normalized); parseErr == nil {
			riskFile = normalizedFile
		}
	}
	return Analysis{
		File:            file,
		RiskFile:        riskFile,
		StrippedHeredoc: stripQuotedHeredocBodiesFile(command, file),
		Canonical:       canonicalForms(file, 0),
		CanonicalExact:  normalized == command,
	}
}
