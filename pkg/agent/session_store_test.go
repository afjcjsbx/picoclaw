package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/session"
)

// newTestSessionStore returns a JSONL-backed session store rooted in a
// temporary directory, with cleanup registered on t.
func newTestSessionStore(t *testing.T) *session.JSONLBackend {
	t.Helper()

	store, err := memory.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	backend := session.NewJSONLBackend(store)
	t.Cleanup(func() { _ = backend.Close() })
	return backend
}
