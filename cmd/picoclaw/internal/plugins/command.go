package plugins

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal"
	"github.com/sipeed/picoclaw/pkg/config"
	pluginpkg "github.com/sipeed/picoclaw/pkg/plugins"
)

func NewPluginsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugins",
		Short: "Manage plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd)
		},
	}

	cmd.AddCommand(
		newListCommand(),
		newShowCommand(),
		newEnableCommand(),
		newDisableCommand(),
	)

	return cmd
}

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List discovered plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd)
		},
	}
}

func newShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show plugin details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, result, err := loadDiscovery()
			if err != nil {
				return err
			}
			plugin, ok := result.Find(args[0])
			if !ok {
				return fmt.Errorf("plugin %q was not found", args[0])
			}
			printPluginDetails(cmd, plugin)
			return nil
		},
	}
}

func newEnableCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "enable <name>",
		Short: "Enable a discovered plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, result, err := loadDiscovery()
			if err != nil {
				return err
			}
			plugin, ok := result.Find(args[0])
			if !ok {
				return fmt.Errorf("plugin %q was not found", args[0])
			}
			if plugin.LoadError != nil {
				return fmt.Errorf("plugin %q cannot be enabled: %w", plugin.Key, plugin.LoadError)
			}
			cfg.Plugins.Enabled = appendUniquePluginName(cfg.Plugins.Enabled, plugin.Key)
			cfg.Plugins.Disabled = removePluginName(cfg.Plugins.Disabled, plugin.Key, plugin.Manifest.Name)
			if err := config.SaveConfig(internal.GetConfigPath(), cfg); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Plugin %q enabled.\n", plugin.Key)
			return nil
		},
	}
}

func newDisableCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "disable <name>",
		Short: "Disable a plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, result, err := loadDiscovery()
			if err != nil {
				return err
			}
			key := args[0]
			if plugin, ok := result.Find(args[0]); ok {
				key = plugin.Key
				cfg.Plugins.Enabled = removePluginName(cfg.Plugins.Enabled, plugin.Key, plugin.Manifest.Name)
			} else {
				cfg.Plugins.Enabled = removePluginName(cfg.Plugins.Enabled, key)
			}
			cfg.Plugins.Disabled = appendUniquePluginName(cfg.Plugins.Disabled, key)
			if err := config.SaveConfig(internal.GetConfigPath(), cfg); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Plugin %q disabled.\n", key)
			return nil
		},
	}
}

func runList(cmd *cobra.Command) error {
	_, result, err := loadDiscovery()
	if err != nil {
		return err
	}
	if len(result.Plugins) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No plugins discovered.")
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%-28s %-12s %-10s %s\n", "NAME", "STATE", "SOURCE", "DESCRIPTION")
	for _, plugin := range result.Plugins {
		desc := plugin.Manifest.Description
		if plugin.LoadError != nil {
			desc = "error: " + plugin.LoadError.Error()
		}
		fmt.Fprintf(
			cmd.OutOrStdout(),
			"%-28s %-12s %-10s %s\n",
			plugin.Key,
			plugin.State,
			plugin.Source,
			desc,
		)
	}
	return nil
}

func loadDiscovery() (*config.Config, pluginpkg.DiscoveryResult, error) {
	cfg, err := internal.LoadConfig()
	if err != nil {
		return nil, pluginpkg.DiscoveryResult{}, fmt.Errorf("failed to load config: %w", err)
	}
	result := pluginpkg.Discover(pluginpkg.DefaultDiscoveryOptions(cfg))
	return cfg, result, nil
}

func printPluginDetails(cmd *cobra.Command, plugin pluginpkg.Plugin) {
	fmt.Fprintf(cmd.OutOrStdout(), "Name: %s\n", plugin.Key)
	fmt.Fprintf(cmd.OutOrStdout(), "State: %s\n", plugin.State)
	fmt.Fprintf(cmd.OutOrStdout(), "Source: %s\n", plugin.Source)
	fmt.Fprintf(cmd.OutOrStdout(), "Path: %s\n", plugin.Dir)
	if plugin.LoadError != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Error: %v\n", plugin.LoadError)
		return
	}
	if plugin.Manifest.Version != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Version: %s\n", plugin.Manifest.Version)
	}
	if plugin.Manifest.Description != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Description: %s\n", plugin.Manifest.Description)
	}
	if len(plugin.Manifest.RequiresEnv) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Requires env: %s\n", strings.Join(plugin.Manifest.RequiresEnv, ", "))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Tools: %d\n", len(plugin.Manifest.Tools))
	fmt.Fprintf(cmd.OutOrStdout(), "Commands: %d\n", len(plugin.Manifest.Commands))
	fmt.Fprintf(cmd.OutOrStdout(), "Process hooks: %d\n", len(plugin.Manifest.Hooks.Processes))
	fmt.Fprintf(cmd.OutOrStdout(), "MCP servers: %d\n", len(plugin.Manifest.MCPServers))
	fmt.Fprintf(cmd.OutOrStdout(), "Skills: %d\n", len(plugin.Manifest.Skills))
}

func appendUniquePluginName(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}

func removePluginName(values []string, names ...string) []string {
	if len(values) == 0 {
		return values
	}
	remove := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" {
			remove[name] = struct{}{}
		}
	}
	out := values[:0]
	for _, value := range values {
		if _, ok := remove[strings.ToLower(strings.TrimSpace(value))]; ok {
			continue
		}
		out = append(out, value)
	}
	return out
}
