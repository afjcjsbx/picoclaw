package acp

import (
	"strings"

	"github.com/sipeed/picoclaw/pkg/commands"
)

func (r *Runtime) notifyCommands(state *acpSession) error {
	definitions := commands.BuiltinDefinitions()
	custom := commands.LoadCustomCommands(r.workspace)
	definitions = commands.MergeCustomCommandDefinitions(definitions, custom)

	available := make([]AvailableCommand, 0, len(definitions))
	for _, definition := range definitions {
		if acpCommandHidden(definition.Name) {
			continue
		}
		command := AvailableCommand{
			Name:        definition.Name,
			Description: definition.Description,
		}
		if usage := strings.TrimSpace(definition.Usage); usage != "" {
			command.Input = &AvailableCommandInput{Hint: usage}
		}
		available = append(available, command)
	}
	return r.peer.Notify("session/update", SessionNotification{
		SessionID: state.id,
		Update: map[string]any{
			"sessionUpdate":     "available_commands_update",
			"availableCommands": available,
		},
	})
}

func acpCommandHidden(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "switch", "use", "reload", "voice":
		return true
	default:
		return false
	}
}
