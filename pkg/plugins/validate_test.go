package plugins

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

const validMCP = `{"$schema":"` + MCPSchema + `","mcpServers":{"api":{"type":"streamable-http","url":"https://example.org/mcp"}}}`

func hookExtension(hooks ...map[string]any) map[string]any {
	return map[string]any{"extensions": map[string]any{ExtensionNamespace: map[string]any{"hooks": hooks}}}
}

func TestValidate(t *testing.T) {
	good := "---\nname: good\ndescription: Good skill\n---\nBody."
	for _, test := range []struct {
		name     string
		manifest map[string]any
		files    map[string]string
		entry    config.PluginEntryConfig
		fatal    bool
		want     []string // substrings, one per expected diagnostic
	}{
		{name: "clean", files: map[string]string{"skills/good/SKILL.md": good, "mcp.json": validMCP}},
		{name: "unknown manifest field", manifest: map[string]any{"tools": true}, want: []string{`unknown field "tools"`}},
		{name: "bad manifest", manifest: map[string]any{"name": "Bad Name"}, fatal: true, want: nil},
		{name: "bad skill", files: map[string]string{"skills/bad/SKILL.md": "---\nname: other\ndescription: x\n---\n"}, want: []string{"skill name must match"}},
		{
			name:     "hook needs server",
			manifest: hookExtension(map[string]any{"name": "h", "intercept": []string{"before_llm"}, "mcp": map[string]any{"server": "api", "tool": "t"}}),
			want:     []string{`MCP server "api" is not configured`},
		},
		{
			name:     "hook server from override",
			manifest: hookExtension(map[string]any{"name": "h", "intercept": []string{"before_llm"}, "mcp": map[string]any{"server": "api", "tool": "t"}}),
			entry: config.PluginEntryConfig{MCPOverrides: map[string]json.RawMessage{
				"api": json.RawMessage(`{"type":"streamable-http","url":"https://example.org/mcp"}`),
			}},
		},
		{
			// A hook that can never run on before_llm only: observe + append is rejected by the host.
			name:     "bad hook result stage",
			manifest: hookExtension(map[string]any{"name": "h", "observe": []string{"before_llm"}, "mcp": map[string]any{"server": "api", "tool": "t", "result": "append_to_user_message"}}),
			files:    map[string]string{"mcp.json": validMCP},
			want:     []string{"can append results only on before_llm"},
		},
		{name: "bad mcp json", files: map[string]string{"mcp.json": `{"$schema":"x"}`}, want: []string{"mcp: missing or unsupported MCP $schema"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, _ := canonicalRoot(t.TempDir())
			writeTestFile(t, root, "plugin.json", manifestJSON(test.manifest))
			for name, content := range test.files {
				writeTestFile(t, root, name, []byte(content))
			}
			report, err := Validate("", root, test.entry)
			if (err != nil) != test.fatal {
				t.Fatalf("err=%v", err)
			}
			if len(report.Diagnostics) < len(test.want) {
				t.Fatalf("diagnostics=%v, want %v", report.Diagnostics, test.want)
			}
			for i, want := range test.want {
				if got := report.Diagnostics[i].Component + ": " + report.Diagnostics[i].Message; !strings.Contains(
					got,
					want,
				) {
					t.Fatalf("diagnostic %d = %q, want %q", i, got, want)
				}
			}
			if !test.fatal && len(report.Diagnostics) != len(test.want) {
				t.Fatalf("diagnostics=%v, want %v", report.Diagnostics, test.want)
			}
		})
	}
}

func TestValidateReportsCapabilities(t *testing.T) {
	root, _ := canonicalRoot(t.TempDir())
	writeTestFile(t, root, "plugin.json", manifestJSON(hookExtension(
		map[string]any{
			"name":      "recall",
			"intercept": []string{"before_llm"},
			"mcp":       map[string]any{"server": "api", "tool": "t"},
		},
	)))
	writeTestFile(t, root, "mcp.json", []byte(validMCP))
	writeTestFile(t, root, "skills/good/SKILL.md", []byte("---\nname: good\ndescription: Good skill\n---\n"))
	// AllowHooks stays false: validation must still see the hook.
	report, err := Validate("alias", root, config.PluginEntryConfig{})
	if err != nil || len(report.Diagnostics) != 0 {
		t.Fatalf("err=%v diagnostics=%v", err, report.Diagnostics)
	}
	if got := strings.Join(report.Skills, ","); got != "alias:good" {
		t.Fatalf("skills=%q", got)
	}
	if got := strings.Join(report.Servers, ","); got != "api" {
		t.Fatalf("servers=%q", got)
	}
	if got := strings.Join(report.Hooks, ","); got != "alias:recall" {
		t.Fatalf("hooks=%q", got)
	}
}

func TestValidateAppliesSkillNames(t *testing.T) {
	root, _ := canonicalRoot(t.TempDir())
	writeTestFile(t, root, "plugin.json", manifestJSON(nil))
	for _, name := range []string{"keep", "drop"} {
		writeTestFile(t, root, "skills/"+name+"/SKILL.md", []byte("---\nname: "+name+"\ndescription: Skill\n---\n"))
	}
	for _, test := range []struct {
		names []string
		want  string
	}{{nil, "alias:drop,alias:keep"}, {[]string{"keep"}, "alias:keep"}, {[]string{}, ""}} {
		report, err := Validate("alias", root, config.PluginEntryConfig{SkillNames: test.names})
		if err != nil || strings.Join(report.Skills, ",") != test.want {
			t.Fatalf("skill_names=%v: skills=%v err=%v, want %q", test.names, report.Skills, err, test.want)
		}
	}
}
