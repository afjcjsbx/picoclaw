package plugin

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/plugins"
)

func newValidateCommand() *cobra.Command {
	var strict bool

	cmd := &cobra.Command{
		Use:   "validate <path|id>",
		Short: "Lint a plugin package without starting anything",
		Long: `Check a plugin the way the host would load it: manifest, skills, mcp.json and
hooks. No process is started and no server is contacted; use ` + "`plugin test`" + ` for that.

The argument is a package directory, or the id of a configured or discovered
plugin. An id takes precedence over a directory of the same name; write ./name
to force the directory. Hooks are checked even when allow_hooks is off.

Exit status is 1 when the manifest is rejected. Component problems (a bad
skill, hook or MCP server) and ignored manifest fields are warnings, because
the host loads the rest of the plugin anyway; --strict makes them fail too.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			id, path, entry := "", args[0], config.PluginEntryConfig{}
			if len(lookup(cfg, args[0])) > 0 {
				item, resolveErr := resolve(cfg, args[0])
				if resolveErr != nil {
					return resolveErr
				}
				id, path, entry = args[0], item.Root, cfg.Plugins.Entries[args[0]]
			}

			report, err := plugins.Validate(id, path, entry)
			w := cmd.OutOrStdout()
			if report.Root != "" {
				path = report.Root
			}
			fmt.Fprintf(w, "Validating %s\n", path)
			printDiagnostics(w, report.Diagnostics)
			if err != nil {
				return fmt.Errorf("invalid plugin: %w", err)
			}

			fmt.Fprintf(w, "✓ %s: %d skills, %d MCP servers, %d hooks, %d warnings\n",
				strings.TrimSpace(report.Manifest.Name+" "+report.Manifest.Version),
				len(report.Skills), len(report.Servers), len(report.Hooks), len(report.Diagnostics))
			if strict && len(report.Diagnostics) > 0 {
				return fmt.Errorf("%d warning(s) treated as errors by --strict", len(report.Diagnostics))
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&strict, "strict", false, "Fail on warnings as well as errors")

	return cmd
}
