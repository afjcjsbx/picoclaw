package commands

import (
	"context"
	"fmt"
	"strings"
)

func helpCommand() Definition {
	return Definition{
		Name:        "help",
		Description: "Show this help message",
		Usage:       "/help",
		ReadOnly:    true,
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			var defs []Definition
			if rt != nil && rt.ListDefinitions != nil {
				defs = rt.ListDefinitions()
			} else {
				defs = BuiltinDefinitions()
			}

			target := nthToken(req.Text, 1)
			if target == "" {
				message := formatHelpMessage(defs)
				if len(defs) > 0 {
					message += "\n\nUse /help <command> to see detailed help for a single command."
				}
				return req.Reply(message)
			}

			def, ok := NewRegistry(defs).Lookup(normalizeHelpTarget(target))
			if !ok {
				return req.Reply(formatUnknownHelpTarget(target))
			}
			return req.Reply(formatCommandHelp(def))
		},
	}
}

func formatHelpMessage(defs []Definition) string {
	if len(defs) == 0 {
		return "No commands available."
	}

	lines := make([]string, 0, len(defs))
	for _, def := range defs {
		usage := def.EffectiveUsage()
		if usage == "" {
			usage = "/" + def.Name
		}
		desc := def.Description
		if desc == "" {
			desc = "No description"
		}
		lines = append(lines, fmt.Sprintf("%s - %s", usage, desc))
	}
	return strings.Join(lines, "\n")
}

func formatCommandHelp(def Definition) string {
	usage := def.EffectiveUsage()
	if usage == "" {
		usage = "/" + def.Name
	}
	desc := def.Description
	if desc == "" {
		desc = "No description"
	}

	lines := []string{
		fmt.Sprintf("/%s - %s", def.Name, desc),
		"Usage: " + usage,
	}

	if len(def.Aliases) > 0 {
		aliases := make([]string, 0, len(def.Aliases))
		for _, alias := range def.Aliases {
			aliases = append(aliases, "/"+alias)
		}
		lines = append(lines, "Aliases: "+strings.Join(aliases, ", "))
	}

	if len(def.SubCommands) > 0 {
		lines = append(lines, "", "Subcommands:")
		for _, sub := range def.SubCommands {
			subUsage := "/" + def.Name + " " + sub.Name
			if sub.ArgsUsage != "" {
				subUsage += " " + sub.ArgsUsage
			}
			subDesc := sub.Description
			if subDesc == "" {
				subDesc = "No description"
			}
			lines = append(lines, fmt.Sprintf("  %s - %s", subUsage, subDesc))
		}
	}

	return strings.Join(lines, "\n")
}

func formatUnknownHelpTarget(target string) string {
	display := strings.TrimSpace(target)
	if display != "" && !strings.HasPrefix(display, "/") && !strings.HasPrefix(display, "!") {
		display = "/" + display
	}
	return fmt.Sprintf("Unknown command: %s. Use /help to see all commands.", display)
}

func normalizeHelpTarget(target string) string {
	if name, ok := parseCommandName(target); ok {
		return name
	}
	target = strings.TrimSpace(target)
	if i := strings.Index(target, "@"); i >= 0 {
		target = target[:i]
	}
	return normalizeCommandName(target)
}
