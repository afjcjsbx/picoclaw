package tools

import (
	"context"
	"strings"
	"testing"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

func TestToolRegistry_ExecuteWithContext_Base64GuardExemptsOnlyReadFile(t *testing.T) {
	payload := strings.Repeat("A", 2048)
	tests := []struct {
		toolName string
		want     string
	}{
		{"read_file", payload},
		{"exec", toolshared.LargeBase64OmittedMessage},
		{"mcp_files_read_file", toolshared.LargeBase64OmittedMessage},
	}
	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			r := NewToolRegistry()
			r.SetMaxInlineResultRunes(1 << 20)
			r.Register(&mockRegistryTool{
				name:   tt.toolName,
				desc:   "returns a long base64-like run",
				params: map[string]any{},
				result: SilentResult(payload),
			})

			got := r.ExecuteWithContext(context.Background(), tt.toolName, nil, "telegram", "chat-1", nil)
			if got.ForLLM != tt.want {
				t.Fatalf("ForLLM = %q, want %q", got.ForLLM, tt.want)
			}
		})
	}
}
