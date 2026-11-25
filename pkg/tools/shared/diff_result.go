package toolshared

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/pmezard/go-difflib/difflib"
)

const (
	noContentChangeDiffMessage = "(no content change)"
	noNewlineAtEOFMarker       = `\ No newline at end of file`
	diffPreviewSkippedMessage  = "[diff preview skipped: file too large; call read_file for the full edited contents]"
	diffPreviewTruncatedNote   = "[diff preview truncated; call read_file for the full edited contents]"
	modelDiffTruncatedNote     = "[diff truncated; call read_file for the full edited contents]"
	maxDiffInputBytes          = 64 * 1024
	maxDiffInputLines          = 2000
	maxUserDiffPreviewBytes    = 16 * 1024

	// maxModelDiffRunes bounds the diff echoed back to the model. It stays
	// below the default tools.max_inline_result_chars (16 KiB) once the
	// summary line and fences are added, so an edit result is not spilled to
	// an artifact just because the diff is large. Over-budget diffs keep 40%
	// of the head and 60% of the tail around an explicit omission notice.
	maxModelDiffRunes = 12 * 1024
)

// DiffResult creates a tool result containing a unified diff for a successful
// file edit. Unlike a plain "File edited: <path>" summary, the model-facing
// result carries the diff too, so the assistant can reason about the resulting
// change set — including EOF newline transitions and the exact context lines
// the patch matched — without re-reading the file. Both views are bounded so a
// single edit cannot flood the model context or the chat UI.
//
// Callers that render file content should use DiffResultWithContextRedactor to
// scrub credentials from the model-facing context lines.
func DiffResult(path string, before, after []byte) *ToolResult {
	return DiffResultWithContextRedactor(path, before, after, nil)
}

// DiffResultWithContextRedactor is DiffResult with a scrubber for the
// file-derived context lines of the diff. redactContextLine receives the text
// after the leading space marker and must not add newlines or remove them. It
// runs before the diff is bounded, so a credential that would straddle the
// head+tail cut is masked in full instead of leaving a fragment behind.
//
// Only the model-facing context lines are redacted. Added and removed lines are
// left verbatim because the model supplied them, and the user preview keeps the
// raw diff. A nil redactContextLine behaves exactly like DiffResult.
func DiffResultWithContextRedactor(
	path string,
	before, after []byte,
	redactContextLine func(string) string,
) *ToolResult {
	summary := fmt.Sprintf("File edited: %s", path)
	if exceedsDiffPreviewLimits(before, after) {
		return SilentResult(summary + "\n" + diffPreviewSkippedMessage)
	}

	diff, err := buildUnifiedDiff(path, before, after)
	if err != nil {
		return UserResult(fmt.Sprintf("%s\n[diff unavailable: %v]", summary, err))
	}

	if diff == noContentChangeDiffMessage {
		return &ToolResult{
			ForLLM:  summary + "\n" + noContentChangeDiffMessage,
			ForUser: fmt.Sprintf("%s\n```diff\n%s\n```", summary, diff),
		}
	}

	modelSource := redactDiffContextLines(diff, redactContextLine)
	userDiff, userTruncated := truncateDiffPreview(diff, maxUserDiffPreviewBytes)
	modelDiff, modelTruncated := TruncateHeadTail(modelSource, maxModelDiffRunes, "DIFF")

	// A diff that fits both budgets and needs no redaction is rendered once
	// and shared by the model and the UI: one allocation instead of two
	// identical strings.
	if !userTruncated && !modelTruncated && modelSource == diff {
		content := fmt.Sprintf("%s\n```diff\n%s\n```", summary, diff)
		return &ToolResult{ForLLM: content, ForUser: content}
	}

	llmContent := fmt.Sprintf("%s\n```diff\n%s\n```", summary, modelDiff)
	if modelTruncated {
		llmContent += "\n" + modelDiffTruncatedNote
	}

	userContent := fmt.Sprintf("%s\n```diff\n%s\n```", summary, userDiff)
	if userTruncated {
		userContent += "\n" + diffPreviewTruncatedNote
	}

	return &ToolResult{
		ForLLM:  llmContent,
		ForUser: userContent,
		Silent:  false,
		IsError: false,
		Async:   false,
	}
}

func buildUnifiedDiff(path string, before, after []byte) (string, error) {
	// Write into a strings.Builder instead of GetUnifiedDiffString: the
	// helper converts a bytes.Buffer to a string, which copies the whole diff
	// a second time. Builder.String() reuses the buffer, which matters on
	// devices where a 64 KiB edit can produce a ~128 KiB diff.
	var out strings.Builder
	err := difflib.WriteUnifiedDiff(&out, difflib.UnifiedDiff{
		A:        splitDiffLinesPreservingEOF(before),
		B:        splitDiffLinesPreservingEOF(after),
		FromFile: "a/" + diffDisplayPath(path),
		ToFile:   "b/" + diffDisplayPath(path),
		Context:  3,
	})
	if err != nil {
		return "", err
	}

	diff := strings.TrimRight(out.String(), "\n")
	if diff == "" {
		return noContentChangeDiffMessage, nil
	}

	return diff, nil
}

// redactDiffContextLines applies redact to the file-derived context lines of a
// unified diff. A context line starts with a single space; the marker is
// preserved. Added and removed lines, file headers, hunk headers, and the
// no-newline marker are left untouched. It returns diff unchanged when redact
// is nil or no line changes, so callers can keep the shared model/user string.
func redactDiffContextLines(diff string, redact func(string) string) string {
	if redact == nil {
		return diff
	}

	lines := strings.Split(diff, "\n")
	changed := false
	for i, line := range lines {
		if len(line) == 0 || line[0] != ' ' {
			continue
		}
		content := line[1:]
		redacted := redact(content)
		if redacted != content {
			lines[i] = " " + redacted
			changed = true
		}
	}
	if !changed {
		return diff
	}
	return strings.Join(lines, "\n")
}

func splitDiffLinesPreservingEOF(content []byte) []string {
	if len(content) == 0 {
		return nil
	}

	lines := make([]string, 0, bytes.Count(content, []byte{'\n'})+1)
	lineStart := 0
	for i, b := range content {
		if b != '\n' {
			continue
		}
		lines = append(lines, string(content[lineStart:i+1]))
		lineStart = i + 1
	}
	if lineStart < len(content) {
		lines = append(lines, string(content[lineStart:]))
	}

	if lacksTrailingNewline(content) {
		lines[len(lines)-1] += "\n"
		lines = append(lines, noNewlineAtEOFMarker+"\n")
	}

	return lines
}

func lacksTrailingNewline(content []byte) bool {
	return len(content) > 0 && !bytes.HasSuffix(content, []byte("\n"))
}

func exceedsDiffPreviewLimits(before, after []byte) bool {
	return len(before) > maxDiffInputBytes ||
		len(after) > maxDiffInputBytes ||
		countDiffLines(before) > maxDiffInputLines ||
		countDiffLines(after) > maxDiffInputLines
}

func countDiffLines(content []byte) int {
	if len(content) == 0 {
		return 0
	}

	lines := bytes.Count(content, []byte{'\n'})
	if !bytes.HasSuffix(content, []byte("\n")) {
		lines++
	}
	return lines
}

func truncateDiffPreview(diff string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(diff) <= maxBytes {
		return diff, false
	}

	truncated := diff[:maxBytes]
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}

	lastNewline := strings.LastIndexByte(truncated, '\n')
	if lastNewline > 0 {
		truncated = truncated[:lastNewline]
	}

	truncated = strings.TrimRight(truncated, "\n")
	if truncated == "" {
		truncated = diff[:maxBytes]
		for len(truncated) > 0 && !utf8.ValidString(truncated) {
			truncated = truncated[:len(truncated)-1]
		}
		truncated = strings.TrimRight(truncated, "\n")
	}

	return truncated, true
}

func diffDisplayPath(path string) string {
	displayPath := strings.TrimLeft(filepath.ToSlash(path), "/")
	if displayPath == "" {
		return "file"
	}
	return displayPath
}
