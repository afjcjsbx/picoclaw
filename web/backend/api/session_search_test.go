package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func searchSessionsResponse(
	t *testing.T,
	h *Handler,
	mux *http.ServeMux,
	url string,
) (int, sessionSearchResponse) {
	t.Helper()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	mux.ServeHTTP(rec, req)

	var body sessionSearchResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("Unmarshal() error = %v, body=%s", err, rec.Body.String())
		}
	}
	return rec.Code, body
}

func newSearchTestStore(t *testing.T, dir string) *memory.JSONLStore {
	t.Helper()

	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	return store
}

func addSearchMessage(
	t *testing.T,
	store *memory.JSONLStore,
	sessionKey string,
	msg providers.Message,
) {
	t.Helper()

	if err := store.AddFullMessage(nil, sessionKey, msg); err != nil {
		t.Fatalf("AddFullMessage(%q) error = %v", msg.Role, err)
	}
}

func TestHandleSearchSessions_JSONLFullTextMatchesAndIndex(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store := newSearchTestStore(t, dir)

	sessionKey := legacyPicoSessionPrefix + "search-jsonl"
	addSearchMessage(
		t,
		store,
		sessionKey,
		providers.Message{Role: "user", Content: "How do I deploy to production?"},
	)
	addSearchMessage(
		t,
		store,
		sessionKey,
		providers.Message{Role: "assistant", Content: "Run the deploy script after tests pass."},
	)
	addSearchMessage(
		t,
		store,
		sessionKey,
		providers.Message{Role: "user", Content: "Thanks, that worked."},
	)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	code, body := searchSessionsResponse(t, h, mux, "/api/sessions/search?q=deploy")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(body.Results) != 2 {
		t.Fatalf("len(results) = %d, want 2 (%#v)", len(body.Results), body.Results)
	}

	first := body.Results[0]
	if first.SessionID != "search-jsonl" {
		t.Fatalf("SessionID = %q", first.SessionID)
	}
	if first.Role != "user" {
		t.Fatalf("Role = %q, want user", first.Role)
	}
	if first.MessageIndex != 0 {
		t.Fatalf("MessageIndex = %d, want 0", first.MessageIndex)
	}
	if first.Title != "How do I deploy to production?" {
		t.Fatalf("Title = %q", first.Title)
	}
	if snippet := first.Snippet; snippet == "" || first.MatchStart >= first.MatchEnd {
		t.Fatalf(
			"snippet/match offsets invalid: %q %d..%d",
			snippet,
			first.MatchStart,
			first.MatchEnd,
		)
	}

	second := body.Results[1]
	if second.Role != "assistant" || second.MessageIndex != 1 {
		t.Fatalf("second result = %#v, want assistant index 1", second)
	}
}

func TestHandleSearchSessions_CaseInsensitive(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store := newSearchTestStore(t, dir)

	sessionKey := legacyPicoSessionPrefix + "search-case"
	addSearchMessage(
		t,
		store,
		sessionKey,
		providers.Message{Role: "user", Content: "The Kubernetes cluster is healthy."},
	)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	_, body := searchSessionsResponse(t, h, mux, "/api/sessions/search?q=kubernetes")
	if len(body.Results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(body.Results))
	}
}

func TestHandleSearchSessions_ExcludesToolsAndThoughts(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store := newSearchTestStore(t, dir)

	sessionKey := legacyPicoSessionPrefix + "search-exclude"
	addSearchMessage(
		t,
		store,
		sessionKey,
		providers.Message{Role: "user", Content: "Visible greeting."},
	)
	addSearchMessage(
		t,
		store,
		sessionKey,
		providers.Message{Role: "tool", Content: "forbidden-tool-token"},
	)
	addSearchMessage(t, store, sessionKey, providers.Message{
		Role:             "assistant",
		Content:          "Normal reply.",
		ReasoningContent: "forbidden-thought-token",
	})

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	_, toolBody := searchSessionsResponse(t, h, mux, "/api/sessions/search?q=forbidden-tool-token")
	if len(toolBody.Results) != 0 {
		t.Fatalf("tool results = %#v, want none", toolBody.Results)
	}

	_, thoughtBody := searchSessionsResponse(
		t,
		h,
		mux,
		"/api/sessions/search?q=forbidden-thought-token",
	)
	if len(thoughtBody.Results) != 0 {
		t.Fatalf("thought results = %#v, want none", thoughtBody.Results)
	}
}

func TestHandleSearchSessions_ShortQueryReturnsEmpty(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store := newSearchTestStore(t, dir)
	sessionKey := legacyPicoSessionPrefix + "search-short"
	addSearchMessage(t, store, sessionKey, providers.Message{Role: "user", Content: "hello"})

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	_, body := searchSessionsResponse(t, h, mux, "/api/sessions/search?q=h")
	if len(body.Results) != 0 {
		t.Fatalf("len(results) = %d, want 0", len(body.Results))
	}
}

func TestHandleSearchSessions_PerSessionCap(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store := newSearchTestStore(t, dir)

	sessionKey := legacyPicoSessionPrefix + "search-cap"
	for i := 0; i < 10; i++ {
		addSearchMessage(
			t,
			store,
			sessionKey,
			providers.Message{Role: "user", Content: "repeated needle here"},
		)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	_, body := searchSessionsResponse(t, h, mux, "/api/sessions/search?q=needle")
	if len(body.Results) != maxSessionSearchPerSession {
		t.Fatalf("len(results) = %d, want %d", len(body.Results), maxSessionSearchPerSession)
	}
}

func TestHandleSearchSessions_OffsetPaging(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store := newSearchTestStore(t, dir)

	sessionKey := legacyPicoSessionPrefix + "search-offset"
	addSearchMessage(
		t,
		store,
		sessionKey,
		providers.Message{Role: "user", Content: "alpha keyword one"},
	)
	addSearchMessage(
		t,
		store,
		sessionKey,
		providers.Message{Role: "assistant", Content: "beta keyword two"},
	)
	addSearchMessage(
		t,
		store,
		sessionKey,
		providers.Message{Role: "user", Content: "gamma keyword three"},
	)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	_, firstPage := searchSessionsResponse(t, h, mux, "/api/sessions/search?q=keyword&limit=2")
	if len(firstPage.Results) != 2 || !firstPage.HasMore {
		t.Fatalf("first page = %#v, has_more=%v", firstPage.Results, firstPage.HasMore)
	}

	_, secondPage := searchSessionsResponse(
		t,
		h,
		mux,
		"/api/sessions/search?q=keyword&limit=2&offset=2",
	)
	if len(secondPage.Results) != 1 {
		t.Fatalf("second page len = %d, want 1", len(secondPage.Results))
	}
	if secondPage.Results[0].MessageIndex != firstPage.Results[1].MessageIndex+1 {
		t.Fatalf(
			"offset result index = %d, want %d",
			secondPage.Results[0].MessageIndex,
			firstPage.Results[1].MessageIndex+1,
		)
	}
	if secondPage.HasMore {
		t.Fatalf("second page has_more = true, want false")
	}
}

func TestHandleSearchSessions_MostRecentFirst(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store := newSearchTestStore(t, dir)

	olderKey := legacyPicoSessionPrefix + "search-older"
	addSearchMessage(
		t,
		store,
		olderKey,
		providers.Message{Role: "user", Content: "shared-topic about older"},
	)
	time.Sleep(10 * time.Millisecond)
	newerKey := legacyPicoSessionPrefix + "search-newer"
	addSearchMessage(
		t,
		store,
		newerKey,
		providers.Message{Role: "user", Content: "shared-topic about newer"},
	)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	_, body := searchSessionsResponse(t, h, mux, "/api/sessions/search?q=shared-topic")
	if len(body.Results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(body.Results))
	}
	if body.Results[0].SessionID != "search-newer" {
		t.Fatalf("first SessionID = %q, want search-newer", body.Results[0].SessionID)
	}
}

func TestHandleSearchSessions_NoResults(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store := newSearchTestStore(t, dir)
	sessionKey := legacyPicoSessionPrefix + "search-none"
	addSearchMessage(
		t,
		store,
		sessionKey,
		providers.Message{Role: "user", Content: "nothing relevant"},
	)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	code, body := searchSessionsResponse(t, h, mux, "/api/sessions/search?q=zzzznotfound")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(body.Results) != 0 || body.HasMore {
		t.Fatalf("results = %#v, has_more=%v", body.Results, body.HasMore)
	}
}

func TestHandleSearchSessions_MessageIndexMatchesHistory(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	dir := sessionsTestDir(t, configPath)
	store := newSearchTestStore(t, dir)

	sessionKey := legacyPicoSessionPrefix + "search-index-sync"
	addSearchMessage(t, store, sessionKey, providers.Message{Role: "user", Content: "first user"})
	addSearchMessage(
		t,
		store,
		sessionKey,
		providers.Message{Role: "assistant", Content: "first assistant"},
	)
	addSearchMessage(
		t,
		store,
		sessionKey,
		providers.Message{Role: "user", Content: "target-marker second user"},
	)

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	_, body := searchSessionsResponse(t, h, mux, "/api/sessions/search?q=target-marker")
	if len(body.Results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(body.Results))
	}
	gotIndex := body.Results[0].MessageIndex

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/search-index-sync", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("history status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var detail struct {
		Messages []sessionChatMessage `json:"messages"`
		Start    int                  `json:"start"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("Unmarshal(history) error = %v", err)
	}
	absolute := detail.Start + gotIndex
	if absolute >= len(detail.Messages) {
		t.Fatalf("index %d out of range (len=%d)", absolute, len(detail.Messages))
	}
	if detail.Messages[absolute].Content != "target-marker second user" {
		t.Fatalf("message at index = %q", detail.Messages[absolute].Content)
	}
}
