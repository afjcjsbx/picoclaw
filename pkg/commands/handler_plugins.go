package commands

import (
	"context"
	"fmt"
	"strings"
)

func listPluginsHandler() Handler {
	return func(ctx context.Context, req Request, rt *Runtime) error {
		if rt == nil || rt.ListPlugins == nil {
			return req.Reply(unavailableMsg)
		}

		items := rt.ListPlugins(ctx)
		if len(items) == 0 {
			return req.Reply("No plugins installed")
		}

		header := "Installed Plugins:"
		if rt.Config != nil && !rt.Config.Plugins.Enabled {
			header = "Installed Plugins (plugin host disabled):"
		}

		lines := make([]string, 0, len(items)*5+1)
		lines = append(lines, header)
		for idx, item := range items {
			if idx > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, fmt.Sprintf("- `%s`", item.ID))
			if item.Name != "" && item.Name != item.ID {
				lines = append(lines, fmt.Sprintf("  Name: %s", item.Name))
			}
			lines = append(lines, fmt.Sprintf("  State: %s", item.State))
			if item.Root != "" {
				lines = append(lines, fmt.Sprintf("  Path: %s", item.Root))
			}
			if item.Diagnostics > 0 {
				lines = append(lines, fmt.Sprintf("  Diagnostics: %d", item.Diagnostics))
			}
		}

		return req.Reply(strings.Join(lines, "\n"))
	}
}
