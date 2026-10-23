package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/sipeed/picoclaw/pkg/commands"
)

// slashCommandsResponse is the payload for the chat composer slash palette.
type slashCommandsResponse struct {
	Commands []slashCommandItem `json:"commands"`
}

type slashCommandItem struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	ArgHint     string `json:"arg_hint,omitempty"`
}

func (h *Handler) registerCommandRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/commands", h.handleListCommands)
}

// handleListCommands exposes the same built-in command definitions the agent dispatches on.
func (h *Handler) handleListCommands(w http.ResponseWriter, r *http.Request) {
	defs := commands.BuiltinDefinitions()
	items := make([]slashCommandItem, 0, len(defs))
	for _, def := range defs {
		items = append(items, slashCommandItemFromDefinition(def))
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(slashCommandsResponse{Commands: items}); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

func slashCommandItemFromDefinition(def commands.Definition) slashCommandItem {
	command := "/" + def.Name
	argHint := strings.TrimSpace(strings.TrimPrefix(def.EffectiveUsage(), command))
	return slashCommandItem{
		Command:     command,
		Description: def.Description,
		ArgHint:     argHint,
	}
}
