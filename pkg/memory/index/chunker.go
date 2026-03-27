package index

import (
	"strings"
)

// ChunkResult represents a chunk produced by the chunker.
type ChunkResult struct {
	StartLine int
	EndLine   int
	Text      string
	Hash      string
}

// ChunkMarkdown splits markdown content into chunks with configurable token-based sizing.
// Uses a 4x char-per-token heuristic: maxChars = tokens * 4, overlapChars = overlap * 4.
func ChunkMarkdown(content string, tokens, overlap int) []ChunkResult {
	if tokens <= 0 {
		tokens = 400
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= tokens {
		overlap = tokens - 1
	}

	maxChars := tokens * 4
	overlapChars := overlap * 4

	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return nil
	}

	var chunks []ChunkResult
	var buf strings.Builder
	bufStart := 0
	bufLen := 0

	flush := func(endLine int) {
		text := buf.String()
		if strings.TrimSpace(text) == "" {
			return
		}
		chunks = append(chunks, ChunkResult{
			StartLine: bufStart,
			EndLine:   endLine,
			Text:      text,
			Hash:      HashChunkText(text),
		})
	}

	for i, line := range lines {
		lineLen := len(line) + 1 // +1 for newline

		// If a single line exceeds maxChars, segment it
		if lineLen > maxChars {
			// Flush current buffer first
			if bufLen > 0 {
				flush(i - 1)
				buf.Reset()
				bufLen = 0
			}
			segmentLongLine(line, i, maxChars, overlapChars, &chunks)
			bufStart = i + 1
			continue
		}

		// Would adding this line exceed the limit?
		if bufLen+lineLen > maxChars && bufLen > 0 {
			flush(i - 1)

			// Keep overlap from the end of current buffer
			overlapText := extractOverlap(buf.String(), overlapChars)
			buf.Reset()
			bufLen = 0

			if len(overlapText) > 0 {
				buf.WriteString(overlapText)
				bufLen = len(overlapText)
				// Overlap lines are conceptually from the previous chunk
				// but we track the start of the new content
			}
			bufStart = i
		}

		if bufLen == 0 {
			bufStart = i
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
		bufLen += lineLen
	}

	// Flush remaining
	if bufLen > 0 {
		flush(len(lines) - 1)
	}

	return chunks
}

// segmentLongLine breaks a single long line into sub-chunks.
func segmentLongLine(line string, lineNum, maxChars, overlapChars int, chunks *[]ChunkResult) {
	for start := 0; start < len(line); {
		end := start + maxChars
		if end > len(line) {
			end = len(line)
		}
		text := line[start:end]
		*chunks = append(*chunks, ChunkResult{
			StartLine: lineNum,
			EndLine:   lineNum,
			Text:      text,
			Hash:      HashChunkText(text),
		})
		// Advance with overlap
		advance := maxChars - overlapChars
		if advance <= 0 {
			advance = maxChars
		}
		start += advance
	}
}

// extractOverlap returns the last overlapChars characters from text.
func extractOverlap(text string, overlapChars int) string {
	if overlapChars <= 0 || len(text) <= overlapChars {
		return text
	}
	return text[len(text)-overlapChars:]
}
