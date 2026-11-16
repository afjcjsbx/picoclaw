package shellguard

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Parse parses command with the bash grammar used by the guard. The caller can
// pass the returned file to Guard.CheckFileWithOptions and CheckWorkspaceFile so a
// workspace-restricted exec does not parse the same command twice.
//
// Parse does not normalize the input. CheckWorkspaceFile falls back to its own
// normalized parse when the command contains fixed shell rewrites.
func Parse(command string) (*syntax.File, error) {
	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		// The parser can return a partially populated tree together with an
		// error. A nil file is the contract that makes CheckFileWithOptions and
		// CheckWorkspaceFile reject unparseable input.
		return nil, err
	}
	return file, nil
}
