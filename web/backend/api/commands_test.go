package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/sipeed/picoclaw/pkg/commands"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestHandleListCommands(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/commands", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp slashCommandsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	defs := commands.BuiltinDefinitions()
	if len(resp.Commands) != len(defs) {
		t.Fatalf("len(commands) = %d, want %d", len(resp.Commands), len(defs))
	}

	byCommand := make(map[string]slashCommandItem, len(resp.Commands))
	for _, item := range resp.Commands {
		byCommand[item.Command] = item
	}

	help, ok := byCommand["/help"]
	if !ok {
		t.Fatalf("missing /help in %+v", resp.Commands)
	}
	if help.ArgHint != "" {
		t.Fatalf("/help arg_hint = %q, want empty", help.ArgHint)
	}
}

func TestHandleListCommands_IncludesWorkspaceCustomCommands(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	commandDir := filepath.Join(cfg.WorkspacePath(), commands.CustomCommandsDirName)
	if err := os.MkdirAll(commandDir, 0o755); err != nil {
		t.Fatalf("mkdir commands: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(commandDir, "review.md"),
		[]byte("---\ndescription: Review code\n---\n\nReview $ARGUMENTS."),
		0o644,
	); err != nil {
		t.Fatalf("write custom command: %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/commands", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp slashCommandsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	byCommand := make(map[string]slashCommandItem, len(resp.Commands))
	for _, item := range resp.Commands {
		byCommand[item.Command] = item
	}

	review, ok := byCommand["/review"]
	if !ok {
		t.Fatalf("missing /review in %+v", resp.Commands)
	}
	if review.Description != "Review code" {
		t.Fatalf("/review description = %q, want Review code", review.Description)
	}
	if review.ArgHint != "[arguments]" {
		t.Fatalf("/review arg_hint = %q, want [arguments]", review.ArgHint)
	}
}
