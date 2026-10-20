package schema

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

// legacySnapshot is a literal snapshot in the on-disk format written by the
// legacy session store. The test must not derive the JSON from the Record
// type, otherwise a tag rename would silently change both sides.
const legacySnapshot = `{
  "key": "telegram:123",
  "messages": [
    {
      "role": "user",
      "content": "hello"
    }
  ],
  "summary": "A greeting.",
  "created": "2026-01-02T03:04:05Z",
  "updated": "2026-01-02T03:04:06Z"
}`

func TestRecord_JSONRoundTrip(t *testing.T) {
	var rec Record
	if err := json.Unmarshal([]byte(legacySnapshot), &rec); err != nil {
		t.Fatalf("unmarshal legacy snapshot: %v", err)
	}

	if rec.Key != "telegram:123" {
		t.Errorf("Key = %q, want %q", rec.Key, "telegram:123")
	}
	if len(rec.Messages) != 1 || rec.Messages[0].Role != "user" || rec.Messages[0].Content != "hello" {
		t.Fatalf("Messages = %+v, want one user message with content %q", rec.Messages, "hello")
	}
	if rec.Summary != "A greeting." {
		t.Errorf("Summary = %q, want %q", rec.Summary, "A greeting.")
	}
	wantCreated := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if !rec.Created.Equal(wantCreated) {
		t.Errorf("Created = %v, want %v", rec.Created, wantCreated)
	}
	wantUpdated := time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC)
	if !rec.Updated.Equal(wantUpdated) {
		t.Errorf("Updated = %v, want %v", rec.Updated, wantUpdated)
	}

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal Record: %v", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("unmarshal remarshalled Record: %v", err)
	}
	gotKeys := make([]string, 0, len(fields))
	for k := range fields {
		gotKeys = append(gotKeys, k)
	}
	slices.Sort(gotKeys)
	wantKeys := []string{"created", "key", "messages", "summary", "updated"}
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("marshaled keys = %v, want %v", gotKeys, wantKeys)
	}

	// The round-tripped document must parse back into an identical Record.
	var again Record
	if err := json.Unmarshal(data, &again); err != nil {
		t.Fatalf("unmarshal round-tripped Record: %v", err)
	}
	if again.Key != rec.Key || again.Summary != rec.Summary ||
		!again.Created.Equal(rec.Created) || !again.Updated.Equal(rec.Updated) {
		t.Fatalf("round-tripped Record = %+v, want %+v", again, rec)
	}
	if len(again.Messages) != 1 || again.Messages[0].Role != rec.Messages[0].Role ||
		again.Messages[0].Content != rec.Messages[0].Content {
		t.Fatalf("round-tripped Messages = %+v, want %+v", again.Messages, rec.Messages)
	}
}

func TestRecord_ZeroSummaryOmitted(t *testing.T) {
	data, err := json.Marshal(Record{
		Key:     "telegram:123",
		Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("marshal Record: %v", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("unmarshal Record: %v", err)
	}
	if _, ok := fields["summary"]; ok {
		t.Fatalf("zero summary must be omitted, got %s", data)
	}
	if _, ok := fields["key"]; !ok {
		t.Fatalf("key must always be present, got %s", data)
	}
	if _, ok := fields["messages"]; !ok {
		t.Fatalf("messages must always be present, got %s", data)
	}
}
