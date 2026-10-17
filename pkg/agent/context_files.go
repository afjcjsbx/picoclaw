package agent

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// Workspace context files (AGENT.md/AGENTS.md, SOUL.md, USER.md, IDENTITY.md)
// are injected into the system prompt on every request, so a single oversized
// file would otherwise reclaim a large share of the context window. A file
// above the cap is injected as a head+tail excerpt with a marker in the middle
// that names the omitted sections and points at the read_file call that
// recovers the full text.
const (
	// defaultContextFileMaxChars is the floor of the context-file cap, in
	// Unicode characters (Go runes): the cap used when the model's context
	// window is unknown or too small to raise it.
	defaultContextFileMaxChars = 20_000

	// contextFileMaxCharsCeiling bounds the cap for very large context windows.
	contextFileMaxCharsCeiling = 500_000

	// contextFileWindowPercent is the share of the model's context window the
	// cap may occupy, in percent. Context files share the cached prompt
	// prefix, so the share stays small.
	contextFileWindowPercent = 6

	// contextFileCharsPerToken converts the token-based context window into a
	// character budget.
	contextFileCharsPerToken = 4

	// Head/tail shares of the cap; the middle of the file is dropped.
	contextFileHeadPercent = 70
	contextFileTailPercent = 20

	// contextFileOmittedHeadingsLimit caps the headings named in the marker.
	contextFileOmittedHeadingsLimit = 15
)

// resolveContextFileMaxChars returns the per-file cap for workspace context
// files. A positive configuredMaxChars wins. Otherwise the cap scales with the
// model's context window (contextFileWindowPercent of it, in characters),
// floored at defaultContextFileMaxChars and capped at contextFileMaxCharsCeiling.
func resolveContextFileMaxChars(configuredMaxChars, contextWindow int) int {
	if configuredMaxChars > 0 {
		return configuredMaxChars
	}
	if contextWindow <= 0 {
		return defaultContextFileMaxChars
	}
	budget := contextWindow * contextFileCharsPerToken * contextFileWindowPercent / 100
	return min(max(budget, defaultContextFileMaxChars), contextFileMaxCharsCeiling)
}

// truncateContextFileSection caps an already rendered context-file section.
// The kept text is the head plus the tail of the cap; the marker left in
// between tells the model what was dropped and how to read the full file.
func truncateContextFileSection(section, label, readPath string, maxChars int) string {
	if maxChars <= 0 {
		return section
	}
	runes := []rune(section)
	if len(runes) <= maxChars {
		return section
	}
	headChars := maxChars * contextFileHeadPercent / 100
	tailChars := maxChars * contextFileTailPercent / 100
	omitted := omittedContextFileHeadings(section, headChars, len(runes)-tailChars)
	sections := ""
	if len(omitted) > 0 {
		sections = " Omitted sections: " + strings.Join(omitted, "; ") + "."
	}
	logger.WarnCF("agent", "Context file truncated", map[string]any{
		"file":      label,
		"path":      readPath,
		"chars":     len(runes),
		"max_chars": maxChars,
	})
	marker := fmt.Sprintf(
		"\n\n[...truncated %s: kept %d+%d of %d chars. The middle is omitted.%s "+
			"If you need the full instructions, read the complete file with the read_file tool: %s]\n\n",
		label, headChars, tailChars, len(runes), sections, readPath,
	)
	return string(runes[:headChars]) + marker + string(runes[len(runes)-tailChars:])
}

// contextFileHeadingPattern matches markdown headings (# to ###### followed by
// a space and a non-space character).
var contextFileHeadingPattern = regexp.MustCompile(`^#{1,6} \S`)

// omittedContextFileHeadings lists the markdown headings whose line starts in
// section[start:end) — the part the truncation marker drops — so the model can
// tell what it lost. Heading-looking lines inside fenced code blocks are
// comments, not headings.
func omittedContextFileHeadings(section string, start, end int) []string {
	var headings []string
	offset := 0
	fenced := false
	for line := range strings.Lines(section) {
		stripped := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(stripped, "```"), strings.HasPrefix(stripped, "~~~"):
			fenced = !fenced
		case !fenced && offset >= start && offset < end && contextFileHeadingPattern.MatchString(stripped):
			headings = append(headings, strings.TrimSpace(strings.TrimLeft(stripped, "#")))
		}
		offset += utf8.RuneCountInString(line)
	}
	if len(headings) > contextFileOmittedHeadingsLimit {
		headings = append(headings[:contextFileOmittedHeadingsLimit], "...")
	}
	return headings
}
