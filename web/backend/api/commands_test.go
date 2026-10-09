package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sipeed/picoclaw/pkg/commands"
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

	btw, ok := byCommand["/btw"]
	if !ok {
		t.Fatalf("missing /btw in %+v", resp.Commands)
	}
	if btw.ArgHint != "<question>" {
		t.Fatalf("/btw arg_hint = %q, want %q", btw.ArgHint, "<question>")
	}
	if btw.Description == "" {
		t.Fatalf("/btw description is empty")
	}

	help, ok := byCommand["/help"]
	if !ok {
		t.Fatalf("missing /help in %+v", resp.Commands)
	}
	if help.ArgHint != "" {
		t.Fatalf("/help arg_hint = %q, want empty", help.ArgHint)
	}
}
