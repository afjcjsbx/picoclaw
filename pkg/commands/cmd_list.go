package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func listCommand() Definition {
	return Definition{
		Name:        "list",
		Description: "List available options",
		SubCommands: []SubCommand{
			{
				Name:        "model",
				Description: "Current model and provider",
				Handler:     currentModelHandler(),
			},
			{
				Name:        "models",
				Description: "Configured models",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.Config == nil {
						return req.Reply(unavailableMsg)
					}
					defaultProvider := rt.Config.Agents.Defaults.Provider
					if strings.TrimSpace(defaultProvider) == "" {
						defaultProvider = "openai"
					}
					models := make([]string, 0, len(rt.Config.ModelList))
					for _, model := range rt.Config.ModelList {
						if model == nil || model.IsVirtual() {
							continue
						}
						provider := strings.TrimSpace(model.Provider)
						if provider == "" {
							provider, _ = providers.SplitModelProviderAndID(model.Model, defaultProvider)
						} else {
							provider = providers.NormalizeProvider(provider)
						}
						models = append(models, fmt.Sprintf("%s (Provider: %s)", model.ModelName, provider))
					}
					if len(models) == 0 {
						return req.Reply("No models configured")
					}
					return req.Reply("Configured Models:\n- " + strings.Join(models, "\n- "))
				},
			},
			{
				Name:        "channels",
				Description: "Enabled channels",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.GetEnabledChannels == nil {
						return req.Reply(unavailableMsg)
					}
					enabled := rt.GetEnabledChannels()
					if len(enabled) == 0 {
						return req.Reply("No channels enabled")
					}
					return req.Reply(fmt.Sprintf("Enabled Channels:\n- %s", strings.Join(enabled, "\n- ")))
				},
			},
			{
				Name:        "agents",
				Description: "Registered agents",
				Handler:     agentsHandler(),
			},
			{
				Name:        "skills",
				Description: "Installed skills",
				Handler: func(_ context.Context, req Request, rt *Runtime) error {
					if rt == nil || rt.ListSkillNames == nil {
						return req.Reply(unavailableMsg)
					}
					names := rt.ListSkillNames()
					if len(names) == 0 {
						return req.Reply("No installed skills")
					}
					return req.Reply(fmt.Sprintf(
						"Installed Skills:\n- %s\n\nUse /use <skill> <message> to force one for a single request, or /use <skill> to apply it to your next message.",
						strings.Join(names, "\n- "),
					))
				},
			},
			{
				Name:        "mcp",
				Description: "Configured MCP servers",
				Handler:     listMCPServersHandler(),
			},
		},
	}
}
