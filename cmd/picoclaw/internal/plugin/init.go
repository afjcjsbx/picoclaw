package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/pkg/plugins"
)

func newInitCommand() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "init <name>",
		Short: "Scaffold a new plugin package",
		Long: `Create <dir>/<name> with a plugin.json, a starter skill and a README. The
package passes ` + "`plugin validate`" + ` as generated. The plugin is not added to the
config; do that with ` + "`plugin add`" + `.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !plugins.ValidName(name) {
				return fmt.Errorf("invalid plugin name %q: use 1-64 lowercase letters, digits, '.' or '-'", name)
			}
			root, err := filepath.Abs(filepath.Join(dir, name))
			if err != nil {
				return err
			}
			if entries, readErr := os.ReadDir(root); readErr == nil && len(entries) > 0 {
				return fmt.Errorf("%s already exists and is not empty", root)
			}

			manifest, err := json.MarshalIndent(plugins.PluginManifest{
				Schema:      plugins.ManifestSchema,
				Name:        name,
				Version:     "0.1.0",
				Description: name + " plugin for PicoClaw.",
			}, "", "  ")
			if err != nil {
				return err
			}
			skill := skillName(name)
			files := map[string]string{
				"plugin.json": string(manifest) + "\n",
				filepath.Join("skills", skill, "SKILL.md"): fmt.Sprintf(skillTemplate, skill, name),
				"README.md": fmt.Sprintf(readmeTemplate, name),
			}
			for rel, content := range files {
				path := filepath.Join(root, rel)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					return err
				}
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "✓ Created plugin %q in %s\n\nNext:\n", name, root)
			fmt.Fprintf(w, "  picoclaw plugin validate %s\n", root)
			fmt.Fprintf(w, "  picoclaw plugin add %s --path %s --enable\n", name, root)
			fmt.Fprintf(w, "  picoclaw plugin test %s\n", name)
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "plugins", "Parent directory for the new package")

	return cmd
}

// skillName derives a valid Agent Skills name from a plugin name: skills allow
// no periods, and collapsing the hyphens they become keeps "--" out.
func skillName(plugin string) string {
	name := strings.ReplaceAll(plugin, ".", "-")
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	return name
}

const skillTemplate = `---
name: %s
description: Describe what this skill does and when the agent should use it.
---

# %s

Write the instructions the agent follows when this skill applies.
`

const readmeTemplate = `# %[1]s

A PicoClaw plugin: skills, MCP servers and optional hooks, declared in
` + "`plugin.json`" + `. See docs/guides/writing-a-plugin.md in the PicoClaw repository.

## Develop

` + "```sh" + `
picoclaw plugin validate .          # lint the package, starts nothing
picoclaw plugin add %[1]s --path . --enable
picoclaw plugin test %[1]s          # dry-run load, lists tools and diagnostics
` + "```" + `

Restart the gateway after enabling the plugin.
`
