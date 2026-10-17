package toolshared

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// HeadTailHeadRatio is the share of the truncation budget kept from the start
// of oversized tool output; the rest is kept from the end. Errors surface early
// while the most recent lines matter most, so the tail gets the larger share.
const HeadTailHeadRatio = 0.4

// TruncateHeadTail bounds text to maxRunes when it is longer: the first 40% and
// the last 60% of the budget are kept around a single omission notice, so the
// kept text is exactly maxRunes long and the notice rides on top. It returns
// the text unchanged, with false, when it already fits or when maxRunes is not
// positive. Both cuts are rune-safe.
//
// The notice wording is the shared "<LABEL> TRUNCATED - N chars omitted out of
// T total" shape, so downstream code that recognizes the marker sees one form
// regardless of which tool produced the output.
func TruncateHeadTail(text string, maxRunes int, label string) (string, bool) {
	total := utf8.RuneCountInString(text)
	if maxRunes <= 0 || total <= maxRunes {
		return text, false
	}

	headBudget := int(float64(maxRunes) * HeadTailHeadRatio)
	tailBudget := maxRunes - headBudget
	notice := fmt.Sprintf(
		"\n\n... [%s TRUNCATED - %s chars omitted out of %s total] ...\n\n",
		label, formatCount(total-headBudget-tailBudget), formatCount(total),
	)

	return RunePrefix(text, headBudget) + notice + RuneSuffix(text, tailBudget), true
}

// RunePrefix returns the first n runes of s, cut on a rune boundary.
func RunePrefix(s string, n int) string {
	if n <= 0 {
		return ""
	}
	end := 0
	for i := 0; i < n && end < len(s); i++ {
		_, width := utf8.DecodeRuneInString(s[end:])
		end += width
	}
	return s[:end]
}

// RuneSuffix returns the last n runes of s, cut on a rune boundary.
func RuneSuffix(s string, n int) string {
	if n <= 0 {
		return ""
	}
	start := len(s)
	for i := 0; i < n && start > 0; i++ {
		_, width := utf8.DecodeLastRuneInString(s[:start])
		start -= width
	}
	return s[start:]
}

// formatCount renders a non-negative count with thousands separators, matching
// the notice format used across truncation messages (e.g. "48,166").
func formatCount(n int) string {
	digits := fmt.Sprintf("%d", n)
	negative := strings.HasPrefix(digits, "-")
	if negative {
		digits = digits[1:]
	}

	var out strings.Builder
	if negative {
		out.WriteByte('-')
	}
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(digit)
	}
	return out.String()
}
