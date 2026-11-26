//go:build !no_seahorse && !mipsle && !netbsd && !(freebsd && arm)

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/seahorse"
)

func TestHandleDeleteSession_RemovesSeahorseState(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	ctx := context.Background()
	dir := sessionsTestDir(t, configPath)
	sessionKey := legacyPicoSessionPrefix + "delete-seahorse"
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	if addErr := store.AddMessage(ctx, sessionKey, "user", "delete me"); addErr != nil {
		t.Fatalf("AddMessage() error = %v", addErr)
	}

	engine, err := seahorse.NewEngine(seahorse.Config{DBPath: filepath.Join(dir, "seahorse.db")}, nil)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	seahorseStore := engine.GetRetrieval().Store()
	conv, err := seahorseStore.GetOrCreateConversation(ctx, sessionKey)
	if err != nil {
		t.Fatalf("GetOrCreateConversation() error = %v", err)
	}
	if _, addErr := seahorseStore.AddMessage(ctx, conv.ConversationID, "user", "delete me", 5); addErr != nil {
		t.Fatalf("seahorse AddMessage() error = %v", addErr)
	}
	if _, summaryErr := seahorseStore.CreateSummary(ctx, seahorse.CreateSummaryInput{
		ConversationID: conv.ConversationID,
		Kind:           seahorse.SummaryKindLeaf,
		Content:        "delete summary",
		TokenCount:     5,
	}); summaryErr != nil {
		t.Fatalf("CreateSummary() error = %v", summaryErr)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/delete-seahorse", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	base := filepath.Join(dir, sanitizeSessionKey(sessionKey))
	for _, path := range []string{base + ".jsonl", base + ".meta.json"} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("expected %s to be removed, stat err = %v", path, statErr)
		}
	}
	deleted, err := seahorseStore.GetConversationBySessionKey(ctx, sessionKey)
	if err != nil {
		t.Fatalf("GetConversationBySessionKey() error = %v", err)
	}
	if deleted != nil {
		t.Fatalf("Seahorse conversation still present: %#v", deleted)
	}
	messageMatches, err := seahorseStore.SearchMessages(ctx, seahorse.SearchInput{Pattern: "delete"})
	if err != nil {
		t.Fatalf("SearchMessages() error = %v", err)
	}
	if len(messageMatches) != 0 {
		t.Fatalf("deleted messages remain in FTS: %#v", messageMatches)
	}
	summaryMatches, err := seahorseStore.SearchSummaries(ctx, seahorse.SearchInput{Pattern: "delete"})
	if err != nil {
		t.Fatalf("SearchSummaries() error = %v", err)
	}
	if len(summaryMatches) != 0 {
		t.Fatalf("deleted summaries remain in FTS: %#v", summaryMatches)
	}
}

func TestHandleDeleteSession_SeahorseErrorPreservesFiles(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	ctx := context.Background()
	dir := sessionsTestDir(t, configPath)
	sessionKey := legacyPicoSessionPrefix + "delete-seahorse-error"
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore() error = %v", err)
	}
	if addErr := store.AddMessage(ctx, sessionKey, "user", "keep me"); addErr != nil {
		t.Fatalf("AddMessage() error = %v", addErr)
	}
	if writeErr := os.WriteFile(
		filepath.Join(dir, "seahorse.db"),
		[]byte("not a sqlite database"),
		0o600,
	); writeErr != nil {
		t.Fatalf("WriteFile() error = %v", writeErr)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/delete-seahorse-error", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	base := filepath.Join(dir, sanitizeSessionKey(sessionKey))
	if _, statErr := os.Stat(base + ".jsonl"); statErr != nil {
		t.Fatalf("JSONL should remain after Seahorse deletion failure: %v", statErr)
	}
}
