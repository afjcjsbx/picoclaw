package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/agent"
)

const subagentsStatusPath = "/internal/subagents/status"

type subagentStatusTask struct {
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

type subagentStatusResponse struct {
	RuntimeAvailable bool                 `json:"runtime_available"`
	Active           []subagentStatusTask `json:"active"`
}

func subagentsStatusHandler(agentLoop *agent.AgentLoop) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if agentLoop == nil {
			http.Error(w, "agent runtime unavailable", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(buildSubagentStatusResponse(agentLoop))
	})
}

func buildSubagentStatusResponse(agentLoop *agent.AgentLoop) subagentStatusResponse {
	response := subagentStatusResponse{
		RuntimeAvailable: agentLoop != nil,
		Active:           make([]subagentStatusTask, 0),
	}
	if agentLoop == nil {
		return response
	}

	registry := agentLoop.GetRegistry()
	for _, turn := range agentLoop.ListActiveTurns() {
		if turn.Depth <= 0 {
			continue
		}
		response.Active = append(response.Active, subagentStatusTask{
			TurnID:       turn.TurnID,
			AgentID:      turn.AgentID,
			AgentName:    subagentAgentName(registry, turn.AgentID, turn.AgentID),
			ParentTurnID: turn.ParentTurnID,
			SessionKey:   turn.SessionKey,
			Channel:      turn.Channel,
			ChatID:       turn.ChatID,
			Task:         trimSubagentTask(turn.UserMessage),
			Phase:        string(turn.Phase),
			Iteration:    turn.Iteration,
			Depth:        turn.Depth,
			StartedAt:    formatSubagentStart(turn.StartedAt),
		})
	}
	return response
}

func subagentAgentName(registry *agent.AgentRegistry, id, fallback string) string {
	if registry == nil {
		return fallback
	}
	if descriptor, ok := registry.GetAgentDescriptor(id); ok {
		if name := strings.TrimSpace(descriptor.Name); name != "" {
			return name
		}
	}
	if trimmed := strings.TrimSpace(id); trimmed != "" {
		return trimmed
	}
	return fallback
}

func trimSubagentTask(task string) string {
	task = strings.TrimSpace(task)
	if task == "" {
		return ""
	}
	const maxRunes = 600
	runes := []rune(task)
	if len(runes) <= maxRunes {
		return task
	}
	return string(runes[:maxRunes]) + "…"
}

func formatSubagentStart(startedAt time.Time) string {
	if startedAt.IsZero() {
		return ""
	}
	return startedAt.Format(time.RFC3339Nano)
}
