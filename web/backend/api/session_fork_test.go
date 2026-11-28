package api

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestHandleForkSession(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	dir := sessionsTestDir(t, configPath)
	parentID := "fork-parent"
	parentKey := legacyPicoSessionPrefix + parentID
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range []providers.Message{
		{Role: "user", Content: "question"},
		{Role: "assistant", Content: "answer"},
		{Role: "user", Content: "later question"},
		{Role: "assistant", Content: "later answer"},
	} {
		if err := store.AddFullMessage(nil, parentKey, msg); err != nil {
			t.Fatal(err)
		}
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerSessionRoutes(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/sessions/"+parentID+"/fork",
		strings.NewReader(`{"message_index":1}`),
	)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var created struct {
		ID         string `json:"id"`
		ForkedFrom string `json:"forked_from"`
		ForkIndex  int    `json:"fork_index"`
	}
	if decodeErr := json.Unmarshal(rec.Body.Bytes(), &created); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if created.ID == "" || created.ForkedFrom != parentID || created.ForkIndex != 1 {
		t.Fatalf("fork response = %+v", created)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sessions/"+created.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET fork status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var detail struct {
		Messages   []sessionChatMessage `json:"messages"`
		ForkedFrom string               `json:"forked_from"`
		ForkIndex  int                  `json:"fork_index"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 2 || detail.Messages[1].Content != "answer" {
		t.Fatalf("fork messages = %+v", detail.Messages)
	}
	if detail.ForkedFrom != parentID || detail.ForkIndex != 1 {
		t.Fatalf("fork metadata = from %q index %d", detail.ForkedFrom, detail.ForkIndex)
	}
}

func TestHandleForkSessionKeepsToolResults(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	dir := sessionsTestDir(t, configPath)
	parentID := "fork-tool-parent"
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	messages := []providers.Message{
		{Role: "user", Content: "run a tool"},
		{
			Role:    "assistant",
			Content: "tool completed",
			ToolCalls: []providers.ToolCall{{
				ID: "call-1", Type: "function",
				Function: &providers.FunctionCall{Name: "read_file", Arguments: `{"path":"README.md"}`},
			}},
		},
		{Role: "tool", ToolCallID: "call-1", Content: "tool result"},
	}
	for _, msg := range messages {
		if addErr := store.AddFullMessage(nil, legacyPicoSessionPrefix+parentID, msg); addErr != nil {
			t.Fatal(addErr)
		}
	}

	h := NewHandler(configPath)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/sessions/"+parentID+"/fork",
		strings.NewReader(`{"message_index":1}`),
	)
	req.SetPathValue("id", parentID)
	h.handleForkSession(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if decodeErr := json.Unmarshal(rec.Body.Bytes(), &created); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	base := filepath.Join(dir, sanitizeSessionKey(legacyPicoSessionPrefix+created.ID))
	raw, err := os.ReadFile(base + ".jsonl")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 {
		t.Fatalf("fork JSONL lines = %d, want 3: %s", len(lines), raw)
	}
	var last providers.Message
	if err := json.Unmarshal([]byte(lines[2]), &last); err != nil {
		t.Fatal(err)
	}
	if last.Role != "tool" || last.ToolCallID != "call-1" {
		t.Fatalf("last fork message = %+v, want attached tool result", last)
	}
}

func TestHandleForkSessionRejectsInvalidRequests(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	dir := sessionsTestDir(t, configPath)
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	parentID := "fork-invalid-parent"
	for _, msg := range []providers.Message{
		{Role: "user", Content: "question"},
		{Role: "assistant", Content: "answer"},
	} {
		if err := store.AddFullMessage(nil, legacyPicoSessionPrefix+parentID, msg); err != nil {
			t.Fatal(err)
		}
	}
	h := NewHandler(configPath)

	tests := []struct {
		name   string
		id     string
		body   string
		status int
	}{
		{name: "missing id", body: `{"message_index":1}`, status: http.StatusBadRequest},
		{name: "malformed JSON", id: parentID, body: `{`, status: http.StatusBadRequest},
		{name: "negative index", id: parentID, body: `{"message_index":-1}`, status: http.StatusBadRequest},
		{name: "missing session", id: "missing", body: `{"message_index":0}`, status: http.StatusNotFound},
		{name: "index out of range", id: parentID, body: `{"message_index":2}`, status: http.StatusBadRequest},
		{name: "user message", id: parentID, body: `{"message_index":0}`, status: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+tt.id+"/fork", strings.NewReader(tt.body))
			req.SetPathValue("id", tt.id)
			h.handleForkSession(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.status, rec.Body.String())
			}
		})
	}
}

func TestWriteForkSessionRollback(t *testing.T) {
	t.Run("message encoding failure", func(t *testing.T) {
		base := filepath.Join(t.TempDir(), "fork")
		err := writeForkSession(base, []providers.Message{{
			Role:  "assistant",
			Usage: &providers.UsageInfo{CostUSD: math.NaN()},
		}}, memory.SessionMeta{Key: "fork"})
		if err == nil {
			t.Fatal("writeForkSession() error = nil, want encoding error")
		}
		assertForkFilesAbsent(t, base)
	})

	t.Run("metadata write failure", func(t *testing.T) {
		base := filepath.Join(t.TempDir(), "fork")
		if err := os.Mkdir(base+".meta.json", 0o700); err != nil {
			t.Fatal(err)
		}
		err := writeForkSession(
			base,
			[]providers.Message{{Role: "assistant", Content: "answer"}},
			memory.SessionMeta{Key: "fork"},
		)
		if err == nil {
			t.Fatal("writeForkSession() error = nil, want metadata write error")
		}
		assertForkFilesAbsent(t, base)
	})

	t.Run("does not overwrite existing transcript", func(t *testing.T) {
		base := filepath.Join(t.TempDir(), "fork")
		writeFileForTest(t, base+".jsonl", "existing", 0o600)
		err := writeForkSession(
			base,
			[]providers.Message{{Role: "assistant", Content: "answer"}},
			memory.SessionMeta{Key: "fork"},
		)
		if err == nil {
			t.Fatal("writeForkSession() error = nil, want exclusive-create error")
		}
		raw, readErr := os.ReadFile(base + ".jsonl")
		if readErr != nil || string(raw) != "existing" {
			t.Fatalf("existing transcript = %q, %v", raw, readErr)
		}
	})
}

func assertForkFilesAbsent(t *testing.T, base string) {
	t.Helper()
	for _, suffix := range []string{".jsonl", ".meta.json"} {
		if _, err := os.Stat(base + suffix); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after rollback (stat error %v)", base+suffix, err)
		}
	}
}

func TestWriteForkSessionWritesMetadata(t *testing.T) {
	base := filepath.Join(t.TempDir(), "fork")
	now := time.Now().UTC()
	meta := memory.SessionMeta{
		Key: "fork", Count: 1, CreatedAt: now, UpdatedAt: now,
		ForkedFrom: "parent", ForkIndex: 1, ForkTitle: "answer", ForkDepth: 2,
	}
	if err := writeForkSession(base, []providers.Message{{Role: "assistant", Content: "answer"}}, meta); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(base + ".meta.json")
	if err != nil {
		t.Fatal(err)
	}
	var got memory.SessionMeta
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Key != meta.Key || got.ForkedFrom != meta.ForkedFrom || got.ForkIndex != meta.ForkIndex ||
		got.ForkDepth != meta.ForkDepth {
		t.Fatalf("metadata = %+v, want %+v", got, meta)
	}
}
