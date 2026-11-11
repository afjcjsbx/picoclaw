package tools

import (
	"context"
	"strings"
	"testing"
)

func TestToolRegistry_ExecuteWithContext_AllowLargeBase64LikeKeepsPayload(t *testing.T) {
	r := NewToolRegistry()
	r.SetMaxInlineResultRunes(1 << 20)

	payload := strings.Repeat("A", 2048)
	result := SilentResult(payload)
	result.AllowLargeBase64Like = true
	r.Register(&mockRegistryTool{
		name:   "read_file",
		desc:   "returns line-numbered text",
		params: map[string]any{},
		result: result,
	})

	got := r.ExecuteWithContext(
		context.Background(),
		"read_file",
		nil,
		"telegram",
		"chat-1",
		nil,
	)
	if got.ForLLM != payload {
		t.Fatalf("expected the line-mode payload to stay inline, got %q", got.ForLLM)
	}
}
