package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/fileutil"
	"github.com/sipeed/picoclaw/pkg/routing"
)

type subagentToolSettings struct {
	Subagent    bool `json:"subagent"`
	Spawn       bool `json:"spawn"`
	SpawnStatus bool `json:"spawn_status"`
}

type subagentLimits struct {
	MaxDepth              int `json:"max_depth"`
	MaxConcurrent         int `json:"max_concurrent"`
	ConcurrencyTimeoutSec int `json:"concurrency_timeout_sec"`
	DefaultTimeoutMinutes int `json:"default_timeout_minutes"`
	DefaultTokenBudget    int `json:"default_token_budget"`
}

type subagentModelOverride struct {
	Primary   string   `json:"primary"`
	Fallbacks []string `json:"fallbacks,omitempty"`
}

type subagentAgentItem struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name,omitempty"`
	Default        bool                   `json:"default"`
	Implicit       bool                   `json:"implicit,omitempty"`
	Workspace      string                 `json:"workspace"`
	Model          string                 `json:"model,omitempty"`
	ModelFallbacks []string               `json:"model_fallbacks,omitempty"`
	AllowAgents    []string               `json:"allow_agents"`
	SubagentModel  *subagentModelOverride `json:"subagent_model,omitempty"`
}

type subagentDashboardResponse struct {
	Tools      subagentToolSettings `json:"tools"`
	Limits     subagentLimits       `json:"limits"`
	Agents     []subagentAgentItem  `json:"agents"`
	Configured bool                 `json:"configured"`
}

type subagentAgentUpdate struct {
	ID            string                 `json:"id"`
	AllowAgents   []string               `json:"allow_agents"`
	SubagentModel *subagentModelOverride `json:"subagent_model,omitempty"`
}

type subagentDashboardRequest struct {
	Tools  subagentToolSettings  `json:"tools"`
	Limits subagentLimits        `json:"limits"`
	Agents []subagentAgentUpdate `json:"agents"`
}

type subagentActiveTask struct {
	TurnID       string `json:"turn_id"`
	AgentID      string `json:"agent_id"`
	AgentName    string `json:"agent_name,omitempty"`
	ParentTurnID string `json:"parent_turn_id,omitempty"`
	SessionKey   string `json:"session_key,omitempty"`
	Channel      string `json:"channel,omitempty"`
	ChatID       string `json:"chat_id,omitempty"`
	Task         string `json:"task,omitempty"`
	Phase        string `json:"phase"`
	Iteration    int    `json:"iteration"`
	Depth        int    `json:"depth"`
	StartedAt    string `json:"started_at,omitempty"`
}

type subagentActiveResponse struct {
	RuntimeAvailable bool                 `json:"runtime_available"`
	Active           []subagentActiveTask `json:"active"`
}

const gatewaySubagentsStatusPath = "/internal/subagents/status"

var gatewaySubagentsDo = (&http.Client{Timeout: 3 * time.Second}).Do

func (h *Handler) registerSubagentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/agents/subagents", h.handleGetSubagents)
	mux.HandleFunc("GET /api/agents/subagents/active", h.handleGetActiveSubagents)
	mux.HandleFunc("PUT /api/agents/subagents", h.handleUpdateSubagents)
}

func (h *Handler) handleGetSubagents(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, buildSubagentDashboard(cfg))
}

// handleGetActiveSubagents proxies the gateway's internal sub-turn registry so
// the Web UI can show spawned tasks without exposing the protected endpoint.
func (h *Handler) handleGetActiveSubagents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.fetchGatewaySubagentStatus(r))
}

func (h *Handler) fetchGatewaySubagentStatus(r *http.Request) subagentActiveResponse {
	result := subagentActiveResponse{Active: make([]subagentActiveTask, 0)}
	if !h.gatewayAvailableForProxy() {
		return result
	}

	gateway.mu.Lock()
	pidData := gateway.pidData
	gateway.mu.Unlock()
	if pidData == nil || pidData.Token == "" {
		return result
	}

	target := h.gatewayProxyURL()
	target.Path = gatewaySubagentsStatusPath
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		return result
	}
	req.Header.Set("Authorization", "Bearer "+pidData.Token)

	resp, err := gatewaySubagentsDo(req)
	if err != nil {
		return result
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
		return result
	}

	var upstream subagentActiveResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&upstream); err != nil {
		return result
	}
	if upstream.Active == nil {
		upstream.Active = make([]subagentActiveTask, 0)
	}
	return upstream
}

func (h *Handler) handleUpdateSubagents(w http.ResponseWriter, r *http.Request) {
	var req subagentDashboardRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	h.configMu.Lock()
	defer h.configMu.Unlock()

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	if err := applySubagentDashboard(cfg, req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, buildSubagentDashboard(cfg))
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func buildSubagentDashboard(cfg *config.Config) subagentDashboardResponse {
	configured := len(cfg.Agents.List) > 0
	agents := cfg.Agents.List
	if !configured {
		agents = []config.AgentConfig{{ID: routing.DefaultAgentID, Default: true}}
	}

	defaultID := subagentDefaultAgentID(agents)
	knownIDs := make(map[string]struct{}, len(agents))
	for _, agentCfg := range agents {
		knownIDs[routing.NormalizeAgentID(agentCfg.ID)] = struct{}{}
	}

	items := make([]subagentAgentItem, 0, len(agents))
	for _, agentCfg := range agents {
		id := routing.NormalizeAgentID(agentCfg.ID)
		model, fallbacks := effectiveAgentModel(cfg, &agentCfg)
		item := subagentAgentItem{
			ID:             id,
			Name:           strings.TrimSpace(agentCfg.Name),
			Default:        id == defaultID,
			Implicit:       !configured,
			Workspace:      effectiveAgentWorkspace(cfg, &agentCfg),
			Model:          model,
			ModelFallbacks: fallbacks,
			AllowAgents:    []string{},
		}
		if agentCfg.Subagents != nil {
			seenAllow := make(map[string]struct{}, len(agentCfg.Subagents.AllowAgents))
			for _, allowed := range agentCfg.Subagents.AllowAgents {
				if allowed == "*" {
					item.AllowAgents = []string{"*"}
					break
				}
				targetID := routing.NormalizeAgentID(allowed)
				if targetID == id {
					continue
				}
				if _, ok := knownIDs[targetID]; !ok {
					continue
				}
				if _, ok := seenAllow[targetID]; ok {
					continue
				}
				seenAllow[targetID] = struct{}{}
				item.AllowAgents = append(item.AllowAgents, targetID)
			}
			if agentCfg.Subagents.Model != nil {
				item.SubagentModel = &subagentModelOverride{
					Primary:   agentCfg.Subagents.Model.Primary,
					Fallbacks: append([]string(nil), agentCfg.Subagents.Model.Fallbacks...),
				}
			}
		}
		items = append(items, item)
	}

	subTurn := cfg.Agents.Defaults.SubTurn
	return subagentDashboardResponse{
		Tools: subagentToolSettings{
			Subagent:    cfg.Tools.Subagent.Enabled,
			Spawn:       cfg.Tools.Spawn.Enabled,
			SpawnStatus: cfg.Tools.SpawnStatus.Enabled,
		},
		Limits: subagentLimits{
			MaxDepth:              subTurn.MaxDepth,
			MaxConcurrent:         subTurn.MaxConcurrent,
			ConcurrencyTimeoutSec: subTurn.ConcurrencyTimeoutSec,
			DefaultTimeoutMinutes: subTurn.DefaultTimeoutMinutes,
			DefaultTokenBudget:    subTurn.DefaultTokenBudget,
		},
		Agents:     items,
		Configured: configured,
	}
}

// subagentDefaultAgentID mirrors the runtime selection order: an agent with the
// reserved "main" ID wins, then the first agent marked default, then the first
// configured agent.
func subagentDefaultAgentID(agents []config.AgentConfig) string {
	for _, agent := range agents {
		if routing.NormalizeAgentID(agent.ID) == routing.DefaultAgentID {
			return routing.DefaultAgentID
		}
	}
	for _, agent := range agents {
		if agent.Default {
			return routing.NormalizeAgentID(agent.ID)
		}
	}
	if len(agents) > 0 {
		return routing.NormalizeAgentID(agents[0].ID)
	}
	return routing.DefaultAgentID
}

func effectiveAgentModel(cfg *config.Config, agentCfg *config.AgentConfig) (string, []string) {
	if agentCfg != nil && agentCfg.Model != nil && strings.TrimSpace(agentCfg.Model.Primary) != "" {
		return strings.TrimSpace(agentCfg.Model.Primary), append([]string(nil), agentCfg.Model.Fallbacks...)
	}
	return cfg.Agents.Defaults.GetModelName(), append([]string(nil), cfg.Agents.Defaults.ModelFallbacks...)
}

func effectiveAgentWorkspace(cfg *config.Config, agentCfg *config.AgentConfig) string {
	if agentCfg != nil && strings.TrimSpace(agentCfg.Workspace) != "" {
		return fileutil.ExpandHome(strings.TrimSpace(agentCfg.Workspace))
	}
	id := routing.DefaultAgentID
	if agentCfg != nil {
		id = routing.NormalizeAgentID(agentCfg.ID)
	}
	workspace := fileutil.ExpandHome(cfg.Agents.Defaults.Workspace)
	if agentCfg == nil || agentCfg.Default || id == routing.DefaultAgentID {
		return workspace
	}
	return filepath.Join(workspace, "..", "workspace-"+id)
}

func applySubagentDashboard(cfg *config.Config, req subagentDashboardRequest) error {
	if err := validateSubagentLimits(req.Limits); err != nil {
		return err
	}

	// Spawn tools require the shared subagent runtime. Mirror the Tools page
	// behavior so an inconsistent payload cannot disable the dependency.
	cfg.Tools.Subagent.Enabled = req.Tools.Subagent || req.Tools.Spawn || req.Tools.SpawnStatus
	cfg.Tools.Spawn.Enabled = req.Tools.Spawn
	cfg.Tools.SpawnStatus.Enabled = req.Tools.SpawnStatus
	cfg.Agents.Defaults.SubTurn = config.SubTurnConfig{
		MaxDepth:              req.Limits.MaxDepth,
		MaxConcurrent:         req.Limits.MaxConcurrent,
		ConcurrencyTimeoutSec: req.Limits.ConcurrencyTimeoutSec,
		DefaultTimeoutMinutes: req.Limits.DefaultTimeoutMinutes,
		DefaultTokenBudget:    req.Limits.DefaultTokenBudget,
	}

	if len(cfg.Agents.List) == 0 {
		return nil
	}

	indexByID := make(map[string]int, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		id := routing.NormalizeAgentID(cfg.Agents.List[i].ID)
		if _, exists := indexByID[id]; exists {
			return fmt.Errorf("duplicate agent id %q in configuration", id)
		}
		indexByID[id] = i
	}

	for _, update := range req.Agents {
		id := routing.NormalizeAgentID(update.ID)
		index, ok := indexByID[id]
		if !ok {
			return fmt.Errorf("agent %q not found", id)
		}

		allowAgents, err := normalizeSubagentAllowAgents(id, update.AllowAgents, indexByID)
		if err != nil {
			return err
		}
		model := normalizeSubagentModel(update.SubagentModel)
		if len(allowAgents) == 0 && model == nil {
			cfg.Agents.List[index].Subagents = nil
			continue
		}
		cfg.Agents.List[index].Subagents = &config.SubagentsConfig{
			AllowAgents: allowAgents,
			Model:       model,
		}
	}

	return nil
}

func validateSubagentLimits(limits subagentLimits) error {
	if limits.MaxDepth < 0 {
		return fmt.Errorf("subturn.max_depth must be zero or greater")
	}
	if limits.MaxConcurrent < 0 || limits.MaxConcurrent > 32 {
		return fmt.Errorf("subturn.max_concurrent must be between 0 and 32")
	}
	if limits.ConcurrencyTimeoutSec < 0 {
		return fmt.Errorf("subturn.concurrency_timeout_sec must be zero or greater")
	}
	if limits.DefaultTimeoutMinutes < 0 {
		return fmt.Errorf("subturn.default_timeout_minutes must be zero or greater")
	}
	if limits.DefaultTokenBudget < 0 {
		return fmt.Errorf("subturn.default_token_budget must be zero or greater")
	}
	return nil
}

func normalizeSubagentAllowAgents(
	ownerID string,
	allowAgents []string,
	indexByID map[string]int,
) ([]string, error) {
	normalized := make([]string, 0, len(allowAgents))
	seen := make(map[string]struct{}, len(allowAgents))
	for _, raw := range allowAgents {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if value == "*" {
			return []string{"*"}, nil
		}
		id := routing.NormalizeAgentID(value)
		if id == ownerID {
			continue
		}
		if _, ok := indexByID[id]; !ok {
			return nil, fmt.Errorf("agent %q allows unknown agent %q", ownerID, value)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	return normalized, nil
}

func normalizeSubagentModel(model *subagentModelOverride) *config.AgentModelConfig {
	if model == nil {
		return nil
	}
	primary := strings.TrimSpace(model.Primary)
	fallbacks := make([]string, 0, len(model.Fallbacks))
	seen := make(map[string]struct{}, len(model.Fallbacks))
	for _, raw := range model.Fallbacks {
		value := strings.TrimSpace(raw)
		if value == "" || value == primary {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		fallbacks = append(fallbacks, value)
	}
	if primary == "" {
		return nil
	}
	return &config.AgentModelConfig{Primary: primary, Fallbacks: fallbacks}
}
