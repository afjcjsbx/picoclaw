package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestGetSubagentsReturnsImplicitMainAgent(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerSubagentRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agents/subagents", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var resp subagentDashboardResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Configured {
		t.Fatal("expected implicit agent configuration")
	}
	if len(resp.Agents) != 1 || resp.Agents[0].ID != "main" || !resp.Agents[0].Implicit {
		t.Fatalf("agents = %#v, want implicit main", resp.Agents)
	}
}

func TestUpdateSubagentsPersistsSettingsAndPermissions(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "main", Default: true, Workspace: "/tmp/main-agent"},
		{ID: "research", Name: "Research", Workspace: "/tmp/research-agent", Skills: []string{"browser"}},
		{ID: "writer", Name: "Writer", Model: &config.AgentModelConfig{Primary: "fast"}},
	}
	if saveErr := config.SaveConfig(configPath, cfg); saveErr != nil {
		t.Fatal(saveErr)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerSubagentRoutes(mux)

	body := `{
		"tools": {"subagent": false, "spawn": true, "spawn_status": true},
		"limits": {
			"max_depth": 4,
			"max_concurrent": 6,
			"concurrency_timeout_sec": 45,
			"default_timeout_minutes": 10,
			"default_token_budget": 12000
		},
		"agents": [
			{"id": "main", "allow_agents": ["research", "research", "main"]},
			{"id": "research", "allow_agents": ["*", "writer"]},
			{"id": "writer", "allow_agents": ["main"], "subagent_model": {"primary": "research-model", "fallbacks": ["fast", "research-model", "backup"]}}
		]
	}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/agents/subagents", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Tools.Subagent.Enabled || !updated.Tools.Spawn.Enabled || !updated.Tools.SpawnStatus.Enabled {
		t.Fatalf("subagent tools = %#v, want all enabled", updated.Tools)
	}
	if updated.Agents.Defaults.SubTurn != (config.SubTurnConfig{
		MaxDepth:              4,
		MaxConcurrent:         6,
		ConcurrencyTimeoutSec: 45,
		DefaultTimeoutMinutes: 10,
		DefaultTokenBudget:    12000,
	}) {
		t.Fatalf("subturn = %#v", updated.Agents.Defaults.SubTurn)
	}

	main := updated.Agents.List[0]
	if main.Subagents == nil || len(main.Subagents.AllowAgents) != 1 || main.Subagents.AllowAgents[0] != "research" {
		t.Fatalf("main subagents = %#v", main.Subagents)
	}
	research := updated.Agents.List[1]
	if research.Subagents == nil ||
		len(research.Subagents.AllowAgents) != 1 ||
		research.Subagents.AllowAgents[0] != "*" {
		t.Fatalf("research subagents = %#v", research.Subagents)
	}
	if research.Name != "Research" || len(research.Skills) != 1 || research.Skills[0] != "browser" {
		t.Fatalf("unrelated research fields were not preserved: %#v", research)
	}
	writer := updated.Agents.List[2]
	if writer.Subagents == nil || writer.Subagents.Model == nil {
		t.Fatalf("writer subagents = %#v", writer.Subagents)
	}
	if writer.Subagents.Model.Primary != "research-model" ||
		len(writer.Subagents.Model.Fallbacks) != 2 ||
		writer.Subagents.Model.Fallbacks[0] != "fast" ||
		writer.Subagents.Model.Fallbacks[1] != "backup" {
		t.Fatalf("writer model override = %#v", writer.Subagents.Model)
	}
	if len(writer.Subagents.AllowAgents) != 1 || writer.Subagents.AllowAgents[0] != "main" {
		t.Fatalf("writer allow agents = %#v", writer.Subagents.AllowAgents)
	}
}

func TestGetActiveSubagentsWithoutGateway(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerSubagentRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agents/subagents/active", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"runtime_available"`) {
		t.Fatalf("active endpoint returned the wrong payload: %s", rec.Body.String())
	}

	var resp subagentActiveResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.RuntimeAvailable {
		t.Fatal("RuntimeAvailable = true, want false without a gateway")
	}
	if resp.Active == nil || len(resp.Active) != 0 {
		t.Fatalf("active = %#v, want empty slice", resp.Active)
	}
}

func TestUpdateSubagentsRejectsUnknownTarget(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Agents.List = []config.AgentConfig{{ID: "main", Default: true}}
	if saveErr := config.SaveConfig(configPath, cfg); saveErr != nil {
		t.Fatal(saveErr)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerSubagentRoutes(mux)

	body := `{
		"tools": {"subagent": true, "spawn": true, "spawn_status": false},
		"limits": {},
		"agents": [{"id": "main", "allow_agents": ["missing"]}]
	}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/agents/subagents", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Agents.List[0].Subagents != nil {
		t.Fatalf("invalid request should not persist subagents: %#v", updated.Agents.List[0].Subagents)
	}
}

func TestGetActiveSubagentsFallsBackWhenGatewayIsUnavailable(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerSubagentRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/agents/subagents/active", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var resp subagentActiveResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.RuntimeAvailable {
		t.Fatal("RuntimeAvailable = true, want false")
	}
	if resp.Active == nil || len(resp.Active) != 0 {
		t.Fatalf("active = %#v, want empty slice", resp.Active)
	}
}
