package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/plugins"
)

func loadConfig() (*config.Config, error) {
	cfg, err := internal.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return cfg, nil
}

func saveConfig(cfg *config.Config) error {
	if err := config.SaveConfig(internal.GetConfigPath(), cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	return nil
}

// resolve resolves id to exactly one plugin the host knows about, configured or
// merely present under a plugin directory, and to a package root on disk.
func resolve(cfg *config.Config, id string) (plugins.Installed, error) {
	found := lookup(cfg, id)
	switch {
	case len(found) == 0:
		return plugins.Installed{}, fmt.Errorf("plugin %q not found", id)
	case len(found) > 1:
		return plugins.Installed{}, fmt.Errorf("ambiguous plugin %q: configure an explicit path", id)
	case found[0].Root == "":
		return plugins.Installed{}, fmt.Errorf("plugin %q matches no single package under the plugin directories", id)
	}
	return found[0], nil
}

func lookup(cfg *config.Config, id string) []plugins.Installed {
	var found []plugins.Installed
	for _, item := range plugins.Inventory(cfg.Plugins, cfg.WorkspacePath(), nil) {
		if item.ID == id {
			found = append(found, item)
		}
	}
	return found
}

// runManager loads every enabled entry and returns the statuses reached before
// ctx ends. Statuses are read before Close, which marks every plugin closed.
// Diagnostics are reported by the callers, so the host's own logging is muted.
func runManager(
	ctx context.Context,
	cfg config.PluginsConfig,
	workspace string,
	publish plugins.Publish,
) []plugins.Status {
	logger.SetLevel(logger.ERROR)
	m := plugins.NewManager(cfg, workspace, publish)
	defer m.Close()
	_ = m.Wait(ctx)
	return m.Statuses()
}

// probeResult is what one plugin published during a dry-run load.
type probeResult struct {
	Status plugins.Status
	Skills []string
	Tools  []string
	Hooks  []string
}

// probe loads a single plugin, enabled or not, exactly as the gateway would.
// It starts the plugin's MCP servers; nothing is registered with an agent.
func probe(ctx context.Context, cfg *config.Config, id string, entry config.PluginEntryConfig) probeResult {
	var result probeResult
	entry.Enabled = true
	only := cfg.Plugins
	only.Enabled = true
	only.Entries = map[string]config.PluginEntryConfig{id: entry}

	logger.SetLevel(logger.ERROR)
	m := plugins.NewManager(
		only,
		cfg.WorkspacePath(),
		func(_ context.Context, c plugins.Capabilities) []plugins.Diagnostic {
			for _, skill := range c.Skills {
				result.Skills = append(result.Skills, skill.Info.Name)
			}
			for _, tool := range c.Tools {
				result.Tools = append(result.Tools, tool.Server+"/"+tool.RemoteName())
			}
			for _, hook := range c.Hooks {
				result.Hooks = append(result.Hooks, hook.Name)
			}
			return nil
		},
	)
	_ = m.Wait(ctx)
	statuses := m.Statuses()
	// Close joins the loaders, so result is complete and unshared afterwards.
	_ = m.Close()
	for _, status := range statuses {
		if status.ID == id {
			result.Status = status
		}
	}
	return result
}

func printDiagnostics(w io.Writer, diagnostics []plugins.Diagnostic) {
	for _, d := range diagnostics {
		fmt.Fprintf(w, "  ! %s: %s\n", d.Component, d.Message)
	}
}

func printDiagnosticSection(w io.Writer, diagnostics []plugins.Diagnostic) {
	fmt.Fprintf(w, "Diagnostics (%d)\n", len(diagnostics))
	printDiagnostics(w, diagnostics)
}

func printField(w io.Writer, label, value string) {
	if value != "" {
		fmt.Fprintf(w, "%-13s%s\n", label+":", value)
	}
}

func printList(w io.Writer, label string, values []string) {
	fmt.Fprintf(w, "%s (%d)\n", label, len(values))
	for _, v := range values {
		fmt.Fprintf(w, "  %s\n", v)
	}
}

// shortPath abbreviates the home directory so list columns stay narrow.
func shortPath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" &&
		strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~" + path[len(home):]
	}
	return path
}

func confirmOverwrite(r io.Reader, w io.Writer, id string) (bool, error) {
	if _, err := fmt.Fprintf(w, "Plugin entry %q already exists. Overwrite? [y/N]: ", id); err != nil {
		return false, err
	}
	var answer string
	if _, err := fmt.Fscanln(r, &answer); err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes", nil
}
