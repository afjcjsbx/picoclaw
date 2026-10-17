package toolshared

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPageableMCPServerText_UnwrapsResultWithMetadata(t *testing.T) {
	body := "# Report\n\n" + strings.Repeat("pageable content line\n", 500)
	payload, err := json.Marshal(map[string]any{
		"result":            body,
		"structuredContent": map[string]any{"answer": 42},
	})
	if err != nil {
		t.Fatalf("failed to build payload: %v", err)
	}

	got := PageableMCPServerText(string(payload))

	if !strings.HasPrefix(got, body) {
		prefix := got
		if len(prefix) > 80 {
			prefix = prefix[:80]
		}
		t.Fatalf("expected the wrapped text first, got %q", prefix)
	}
	if !strings.Contains(got, "\n\n"+MCPResultMetadataTag+"\n") {
		t.Fatalf("expected the metadata block, got %q", got)
	}
	if !strings.Contains(got, `"structuredContent"`) || !strings.Contains(got, `"answer": 42`) {
		t.Fatalf("expected structuredContent preserved in the metadata block, got %q", got)
	}
	if !strings.HasSuffix(got, MCPResultMetadataClosingTag+"\n") {
		t.Fatalf("expected the metadata block to close the text, got %q", got)
	}
	if strings.Contains(got, `\n`) {
		t.Fatalf("expected real newlines, got escaped ones: %q", got)
	}
}

func TestPageableMCPServerText_ResultOnlyEnvelopeIsJustTheText(t *testing.T) {
	body := "line one\nline two"
	payload, err := json.Marshal(map[string]any{"result": body})
	if err != nil {
		t.Fatalf("failed to build payload: %v", err)
	}

	got := PageableMCPServerText(string(payload))

	if got != body {
		t.Fatalf("expected the bare result text, got %q", got)
	}
}

func TestPageableMCPServerText_MetaAndStructuredContentPreserved(t *testing.T) {
	payload := `{"result":"body","_meta":{"source":"server"},"structuredContent":{"k":"v"}}`

	got := PageableMCPServerText(payload)

	if !strings.HasPrefix(got, "body") {
		t.Fatalf("expected the wrapped text first, got %q", got)
	}
	var metadata map[string]json.RawMessage
	block := got[strings.Index(got, MCPResultMetadataTag)+len(MCPResultMetadataTag) : strings.Index(got, MCPResultMetadataClosingTag)]
	if err := json.Unmarshal([]byte(strings.TrimSpace(block)), &metadata); err != nil {
		t.Fatalf("expected parseable metadata JSON, got %q: %v", block, err)
	}
	if _, ok := metadata["_meta"]; !ok {
		t.Fatalf("expected _meta preserved, got %q", block)
	}
	if _, ok := metadata["structuredContent"]; !ok {
		t.Fatalf("expected structuredContent preserved, got %q", block)
	}
	if _, ok := metadata["result"]; ok {
		t.Fatalf("expected result not duplicated in the metadata block, got %q", block)
	}
}

func TestPageableMCPServerText_UnrecognizedContentIsVerbatim(t *testing.T) {
	long := strings.Repeat("x", 200)
	cases := []struct {
		name    string
		payload string
	}{
		{"ordinary tool JSON", `{"foo":"bar","count":3}`},
		{"web_fetch shape", `{"url":"https://example.com","status":200,"text":"page"}`},
		{"unknown sibling key", `{"result":"body","extra":1}`},
		{"structured output without result", `{"answer":42}`},
		{"missing result", `{"structuredContent":{"k":"v"}}`},
		{"empty result", `{"result":""}`},
		{"non-string result", `{"result":{"nested":"object"}}`},
		{"plain text", "not json at all\nsecond line"},
		{"unparseable JSON", `{"result": }`},
		{"array payload", `["result","text"]`},
		{"JSON string", `"` + long + `"`},
		{"leading array", `[{"result":"body"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PageableMCPServerText(tc.payload); got != tc.payload {
				t.Fatalf("expected verbatim content, got %q", got)
			}
		})
	}
}

func TestPageableMCPServerText_AllowsLeadingWhitespace(t *testing.T) {
	payload := " \n\t{\"result\":\"body\"}"

	if got := PageableMCPServerText(payload); got != "body" {
		t.Fatalf("expected the envelope unwrapped, got %q", got)
	}
}

func TestPageableMCPServerText_OversizedPayloadStaysVerbatim(t *testing.T) {
	// The parse cap is 16 MiB; simulate a payload beyond it without allocating
	// the whole body: the guard checks length first, before any parse.
	payload := `{"result":"` + strings.Repeat("a", EnvelopeMaxParseBytes) + `"}`

	if got := PageableMCPServerText(payload); got != payload {
		t.Fatalf("expected the oversized payload verbatim")
	}
}
