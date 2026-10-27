package toolshared

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// TestToolResultJSONShape pins the serialized shape of a tool result: the
// internal-only fields never reach JSON, the documented ones keep their keys.
func TestToolResultJSONShape(t *testing.T) {
	result := &ToolResult{
		ForLLM:             "payload",
		ForUser:            "done",
		IsError:            true,
		Err:                errors.New("boom"),
		Media:              []string{"media://one"},
		Messages:           []providers.Message{{Role: "user", Content: "hi"}},
		ArtifactTags:       []string{"[file:/tmp/a.png]"},
		ResponseHandled:    true,
		LargeResultSpilled: true,
		FullText:           "the full text",
	}

	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}

	for _, key := range []string{
		"for_llm", "for_user", "silent", "is_error", "async",
		"media", "artifact_tags", "response_handled",
	} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("serialized result is missing %q: %s", key, payload)
		}
	}

	for _, key := range []string{"Err", "err", "Messages", "LargeResultSpilled", "FullText"} {
		if _, ok := decoded[key]; ok {
			t.Errorf("serialized result must not contain %q: %s", key, payload)
		}
	}

	if decoded["for_llm"] != "payload" {
		t.Errorf("for_llm = %v, want payload", decoded["for_llm"])
	}
}
