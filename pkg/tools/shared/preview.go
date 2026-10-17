package toolshared

import (
	"fmt"
	"unicode/utf8"
)

const (
	// LargeResultSpillSentinel prefixes a ForLLM value that has been reduced to
	// a bounded artifact preview, so the model and logs can recognize it. It is
	// informational only: idempotency is tracked by ToolResult.LargeResultSpilled,
	// because tool output could contain this text.
	LargeResultSpillSentinel = "[picoclaw-large-tool-result]"

	// LargeResultPreviewHeadRunes and LargeResultPreviewTailRunes bound the
	// inline head/tail preview kept for a spilled result. Together they stay
	// within the intended ~2-4KB preview budget: the model usually has enough
	// content to answer without re-reading the artifact, which would otherwise
	// re-spill and loop.
	LargeResultPreviewHeadRunes = 2048
	LargeResultPreviewTailRunes = 1024
)

// BuildLargeResultPreview returns a head+tail preview of text, the number of
// runes omitted between the two fragments, and the head/tail sizes. The preview
// is bounded so inline context stays small while keeping the most useful parts:
// the head (which for content tools may include native truncation/offset
// guidance) and the tail. The middle omission marker carries the real omitted
// count.
//
// Callers must only pass oversized text: total > maxRunes > 0. That guarantees
// headBudget+tailBudget <= maxRunes < total, so something is always omitted and
// both budgets are positive.
func BuildLargeResultPreview(text string, total, maxRunes int) (preview string, omitted, headRunes, tailRunes int) {
	headBudget := LargeResultPreviewHeadRunes
	tailBudget := LargeResultPreviewTailRunes
	if headBudget+tailBudget > maxRunes {
		// Small caps (e.g. tests): scale the preview down so it fits.
		headBudget = maxRunes * 2 / 3
		tailBudget = maxRunes - headBudget
	}

	headRunes = headBudget
	tailRunes = tailBudget
	omitted = total - headBudget - tailBudget

	// Walk only the head and tail windows instead of converting the whole text
	// to []rune: tool output can be tens of MB while the preview is ~3KB.
	headEnd := 0
	for i := 0; i < headBudget; i++ {
		_, w := utf8.DecodeRuneInString(text[headEnd:])
		headEnd += w
	}
	tailStart := len(text)
	for i := 0; i < tailBudget; i++ {
		_, w := utf8.DecodeLastRuneInString(text[:tailStart])
		tailStart -= w
	}

	head, tail := text[:headEnd], text[tailStart:]
	return fmt.Sprintf("%s\n\n[... %d characters truncated ...]\n\n%s", head, omitted, tail),
		omitted, headRunes, tailRunes
}
