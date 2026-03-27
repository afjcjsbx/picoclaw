package index

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"
)

// SessionExtraction holds the result of extracting messages from a JSONL session file.
type SessionExtraction struct {
	Content string // Normalized text content ("User: ...\nAssistant: ...")
	LineMap []int  // Maps normalized line index → original JSONL line number
}

// sessionMessage represents a message entry in a JSONL session file.
type sessionMessage struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// ExtractSessionMessages parses a JSONL session file and extracts user/assistant messages.
// Returns normalized text and a line map for chunk line-number remapping.
func ExtractSessionMessages(content string) (*SessionExtraction, error) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var lines []string
	var lineMap []int
	jsonlLine := 0

	for scanner.Scan() {
		line := scanner.Text()
		jsonlLine++

		if strings.TrimSpace(line) == "" {
			continue
		}

		var msg sessionMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue // Skip invalid JSON lines
		}

		// Only extract user and assistant role messages
		role := strings.ToLower(msg.Role)
		if role != "user" && role != "assistant" {
			continue
		}

		// Extract text content
		text := extractTextContent(msg.Content)
		if text == "" {
			continue
		}

		// Format as "User: <text>" or "Assistant: <text>"
		prefix := "User"
		if role == "assistant" {
			prefix = "Assistant"
		}
		formatted := fmt.Sprintf("%s: %s", prefix, text)

		// Track line mapping
		formattedLines := strings.Split(formatted, "\n")
		for range formattedLines {
			lines = append(lines, "")
			lineMap = append(lineMap, jsonlLine)
		}
		// Replace empty lines with actual content
		startIdx := len(lines) - len(formattedLines)
		for i, fl := range formattedLines {
			lines[startIdx+i] = fl
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan session file: %w", err)
	}

	return &SessionExtraction{
		Content: strings.Join(lines, "\n"),
		LineMap: lineMap,
	}, nil
}

// extractTextContent extracts plain text from various content formats.
func extractTextContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		// Array of content parts (e.g., multimodal messages)
		var parts []string
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if text, ok := m["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, " ")
	case map[string]any:
		if text, ok := v["text"].(string); ok {
			return text
		}
	}
	return ""
}

// RemapChunkLines remaps chunk start/end lines from normalized content lines
// back to original JSONL line numbers using the provided line map.
func RemapChunkLines(startLine, endLine int, lineMap []int) (int, int) {
	if len(lineMap) == 0 {
		return startLine, endLine
	}
	mappedStart := startLine
	mappedEnd := endLine
	if startLine >= 0 && startLine < len(lineMap) {
		mappedStart = lineMap[startLine]
	}
	if endLine >= 0 && endLine < len(lineMap) {
		mappedEnd = lineMap[endLine]
	}
	return mappedStart, mappedEnd
}
