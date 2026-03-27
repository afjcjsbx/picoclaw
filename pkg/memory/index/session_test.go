package index

import (
	"strings"
	"testing"
)

func TestExtractSessionMessages(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantLines  int
		wantUser   bool
		wantAssist bool
	}{
		{
			name:       "user_and_assistant",
			input:      `{"role": "user", "content": "Hello"}` + "\n" + `{"role": "assistant", "content": "Hi there"}` + "\n",
			wantLines:  2,
			wantUser:   true,
			wantAssist: true,
		},
		{
			name:      "system_filtered",
			input:     `{"role": "system", "content": "You are helpful"}` + "\n" + `{"role": "user", "content": "Test"}` + "\n",
			wantLines: 1,
			wantUser:  true,
		},
		{
			name:      "empty_content",
			input:     `{"role": "user", "content": ""}` + "\n",
			wantLines: 0,
		},
		{
			name:      "invalid_json",
			input:     `not json` + "\n" + `{"role": "user", "content": "Valid"}` + "\n",
			wantLines: 1,
			wantUser:  true,
		},
		{
			name:      "multipart_content",
			input:     `{"role": "user", "content": [{"type": "text", "text": "Hello"}, {"type": "text", "text": "World"}]}` + "\n",
			wantLines: 1,
			wantUser:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExtractSessionMessages(tt.input)
			if err != nil {
				t.Fatalf("ExtractSessionMessages: %v", err)
			}

			lines := strings.Split(result.Content, "\n")
			// Remove empty trailing line
			nonEmpty := 0
			for _, l := range lines {
				if l != "" {
					nonEmpty++
				}
			}

			if nonEmpty != tt.wantLines {
				t.Errorf("expected %d non-empty lines, got %d (content: %q)", tt.wantLines, nonEmpty, result.Content)
			}

			if tt.wantUser && !strings.Contains(result.Content, "User:") {
				t.Error("expected 'User:' prefix in content")
			}
			if tt.wantAssist && !strings.Contains(result.Content, "Assistant:") {
				t.Error("expected 'Assistant:' prefix in content")
			}
		})
	}
}

func TestExtractSessionMessages_LineMap(t *testing.T) {
	input := `{"role": "system", "content": "system prompt"}
{"role": "user", "content": "Hello"}
{"role": "assistant", "content": "Hi"}
{"role": "user", "content": "Bye"}
`
	result, err := ExtractSessionMessages(input)
	if err != nil {
		t.Fatalf("ExtractSessionMessages: %v", err)
	}

	if len(result.LineMap) == 0 {
		t.Fatal("expected non-empty line map")
	}

	// First extracted line should map to JSONL line 2 (user message, 1-indexed)
	if result.LineMap[0] != 2 {
		t.Errorf("expected first line to map to JSONL line 2, got %d", result.LineMap[0])
	}
}

func TestRemapChunkLines(t *testing.T) {
	lineMap := []int{2, 3, 3, 4}

	tests := []struct {
		name      string
		start     int
		end       int
		wantStart int
		wantEnd   int
	}{
		{"first", 0, 0, 2, 2},
		{"range", 0, 2, 2, 3},
		{"last", 3, 3, 4, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStart, gotEnd := RemapChunkLines(tt.start, tt.end, lineMap)
			if gotStart != tt.wantStart || gotEnd != tt.wantEnd {
				t.Errorf("RemapChunkLines(%d, %d) = (%d, %d), want (%d, %d)",
					tt.start, tt.end, gotStart, gotEnd, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

func TestRemapChunkLines_EmptyMap(t *testing.T) {
	start, end := RemapChunkLines(5, 10, nil)
	if start != 5 || end != 10 {
		t.Errorf("expected (5, 10) for empty map, got (%d, %d)", start, end)
	}
}
