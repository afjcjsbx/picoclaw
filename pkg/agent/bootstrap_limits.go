package agent

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// maxBootstrapFileReadBytes bounds how much of a single workspace bootstrap
// file is read from disk. The prompt caps are much smaller; this limit only
// protects against accidentally loading a huge file before truncation.
const maxBootstrapFileReadBytes = 2 << 20 // 2 MiB

// bootstrapTruncationNotice is appended whenever a bootstrap section is
// shortened. It names the path so the model can read the full file on demand.
const bootstrapTruncationNotice = "\n\n[bootstrap file truncated: showing %d of %d characters; use read_file on %s for the full content]"

// readBootstrapFile reads at most maxBootstrapFileReadBytes from path. The
// boolean reports whether the file could be opened and read; a longer file is
// truncated to the limit and logged. Invalid UTF-8 at the cut point is trimmed
// so callers never receive a partial rune.
func readBootstrapFile(path string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxBootstrapFileReadBytes+1))
	if err != nil {
		return "", false
	}
	if len(data) > maxBootstrapFileReadBytes {
		data = trimPartialUTF8Suffix(data[:maxBootstrapFileReadBytes])
		logger.WarnCF("agent", "Bootstrap file exceeds read limit", map[string]any{
			"path":        path,
			"limit_bytes": maxBootstrapFileReadBytes,
		})
	}
	return string(data), true
}

// truncateBootstrapText bounds text to maxChars Unicode characters and appends
// a notice pointing at label (a workspace-relative path). The cut is made on a
// rune boundary and the final string, including the notice, never exceeds
// maxChars.
func truncateBootstrapText(text string, maxChars int, label string) (string, bool) {
	total := utf8.RuneCountInString(text)
	if maxChars <= 0 {
		return "", total > 0
	}
	if total <= maxChars {
		return text, false
	}

	if strings.TrimSpace(label) == "" {
		label = "the workspace file"
	}

	// Reserve space for the notice using maxChars for the "shown" count. The
	// real count never has more digits, so the final notice is no longer than
	// the reserved one.
	notice := fmt.Sprintf(bootstrapTruncationNotice, maxChars, total, label)
	keep := maxChars - utf8.RuneCountInString(notice)
	if keep < 0 {
		keep = 0
	}
	prefix := bootstrapRunePrefix(text, keep)
	shown := utf8.RuneCountInString(prefix)
	notice = fmt.Sprintf(bootstrapTruncationNotice, shown, total, label)

	result := prefix + notice
	if utf8.RuneCountInString(result) > maxChars {
		// Only reachable when the per-file cap is smaller than the notice.
		result = bootstrapRunePrefix(result, maxChars)
	}
	return result, true
}

// bootstrapRunePrefix returns the first n runes of s, cut on a rune boundary.
func bootstrapRunePrefix(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// trimPartialUTF8Suffix removes trailing bytes that form an incomplete UTF-8
// sequence. It leaves a deliberately encoded U+FFFD (three bytes) intact.
func trimPartialUTF8Suffix(data []byte) []byte {
	for len(data) > 0 {
		r, size := utf8.DecodeLastRune(data)
		if r != utf8.RuneError || size > 1 {
			return data
		}
		data = data[:len(data)-1]
	}
	return data
}

// appendBootstrapSection renders one capped bootstrap section and returns the
// remaining total budget. Content is capped to maxFileChars first, then to the
// space left in the overall bootstrap block. Headings and separators count
// against the total budget so the rendered block respects maxTotalChars.
func appendBootstrapSection(
	sb *strings.Builder,
	budget, maxFileChars int,
	title, content, label string,
) int {
	if budget <= 0 || strings.TrimSpace(content) == "" {
		return budget
	}

	header := "## " + title + "\n\n"
	const suffix = "\n\n"
	overhead := utf8.RuneCountInString(header) + utf8.RuneCountInString(suffix)
	if budget <= overhead {
		return budget
	}

	contentBudget := budget - overhead
	if maxFileChars > 0 && contentBudget > maxFileChars {
		contentBudget = maxFileChars
	}
	content, _ = truncateBootstrapText(content, contentBudget, label)

	sb.WriteString(header)
	sb.WriteString(content)
	sb.WriteString(suffix)
	return budget - overhead - utf8.RuneCountInString(content)
}

// bootstrapLimits resolves the configured caps, falling back to the documented
// defaults when a value is unset or non-positive.
func (cb *ContextBuilder) bootstrapLimits() (int, int) {
	maxChars := cb.bootstrapMaxChars
	if maxChars <= 0 {
		maxChars = config.DefaultMaxBootstrapChars
	}
	maxTotal := cb.bootstrapMaxTotalChars
	if maxTotal <= 0 {
		maxTotal = config.DefaultMaxBootstrapTotalChars
	}
	return maxChars, maxTotal
}
