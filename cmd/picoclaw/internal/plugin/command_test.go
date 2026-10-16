package plugin

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/plugins"
)

// env isolates one test: its own home, config file and workspace.
type env struct {
	configPath string
	workspace  string
}

func setup(t *testing.T) env {
	t.Helper()
	home := t.TempDir()
	e := env{configPath: filepath.Join(home, "config.json"), workspace: filepath.Join(home, "workspace")}
	t.Setenv(config.EnvConfig, e.configPath)
	t.Setenv(config.EnvHome, home)
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = e.workspace
	require.NoError(t, config.SaveConfig(e.configPath, cfg))
	return e
}

func (e env) load(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.LoadConfig(e.configPath)
	require.NoError(t, err)
	return cfg
}

func (e env) save(t *testing.T, mutate func(*config.PluginsConfig)) {
	t.Helper()
	cfg := e.load(t)
	mutate(&cfg.Plugins)
	require.NoError(t, config.SaveConfig(e.configPath, cfg))
}

// writePackage creates a minimal valid package and returns its directory.
func writePackage(t *testing.T, dir, name string, extra map[string]any) string {
	t.Helper()
	root := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "skills", "greet"), 0o755))
	manifest := map[string]any{"$schema": plugins.ManifestSchema, "name": name}
	for k, v := range extra {
		manifest[k] = v
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "plugin.json"), data, 0o644))
	skill := "---\nname: greet\ndescription: Say hello\n---\nHello.\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "skills", "greet", "SKILL.md"), []byte(skill), 0o644))
	return root
}

func execute(args []string, stdin string) (string, error) {
	var out bytes.Buffer
	cmd := NewPluginCommand()
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	err := cmd.Execute()
	return out.String(), err
}

func TestNewPluginCommand(t *testing.T) {
	cmd := NewPluginCommand()
	names := make([]string, 0, len(cmd.Commands()))
	for _, sub := range cmd.Commands() {
		names = append(names, sub.Name())
		assert.False(t, sub.Hidden)
	}
	slices.Sort(names)
	assert.Equal(
		t,
		[]string{"add", "disable", "edit", "enable", "init", "list", "remove", "show", "test", "validate"},
		names,
	)
}

func TestListStates(t *testing.T) {
	e := setup(t)
	out, err := execute([]string{"list"}, "")
	require.NoError(t, err)
	assert.Contains(t, out, "No plugins configured or found.")

	pluginDir := filepath.Join(e.workspace, "plugins")
	writePackage(t, pluginDir, "on-disk", nil)
	writePackage(t, pluginDir, "waiting", nil)
	writePackage(t, pluginDir, "parked", nil)
	e.save(t, func(p *config.PluginsConfig) {
		p.Enabled = true
		p.Entries = map[string]config.PluginEntryConfig{
			"waiting": {Enabled: true},
			"parked":  {},
		}
	})

	out, err = execute([]string{"list"}, "")
	require.NoError(t, err)
	for _, want := range []string{"on-disk", "not enabled", "waiting", "pending", "parked", "disabled", "| Name"} {
		assert.Contains(t, out, want)
	}
}

func TestListStatusLoadsEnabledPlugins(t *testing.T) {
	e := setup(t)
	writePackage(t, filepath.Join(e.workspace, "plugins"), "live", nil)
	e.save(t, func(p *config.PluginsConfig) {
		p.Enabled = true
		p.DataDir = t.TempDir()
		p.Entries = map[string]config.PluginEntryConfig{"live": {Enabled: true}}
	})

	out, err := execute([]string{"list", "--status"}, "")
	require.NoError(t, err)
	assert.Contains(t, out, "ready")
}

func TestAddWritesEntryAndKeepsOtherSections(t *testing.T) {
	e := setup(t)
	cfg := e.load(t)
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{"keep": {Enabled: true, Command: "npx"}}
	cfg.Gateway.LogLevel = "debug"
	require.NoError(t, config.SaveConfig(e.configPath, cfg))
	before := rawConfig(t, e.configPath)

	t.Chdir(t.TempDir())
	root := writePackage(t, ".", "weather", nil)

	out, err := execute([]string{"add", "weather", "--path", "./weather", "--enable"}, "")
	require.NoError(t, err)
	assert.Contains(t, out, `Plugin "weather" saved and enabled`)

	// A relative --path means relative to the shell, not to the workspace.
	got := e.load(t).Plugins
	assert.True(t, got.Enabled)
	assert.True(t, got.Entries["weather"].Enabled)
	assert.True(t, filepath.IsAbs(got.Entries["weather"].Path))
	resolved, err := filepath.EvalSymlinks(got.Entries["weather"].Path)
	require.NoError(t, err)
	abs, err := filepath.Abs(root)
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(abs)
	require.NoError(t, err)
	assert.Equal(t, want, resolved)

	after := rawConfig(t, e.configPath)
	delete(before, "plugins")
	delete(after, "plugins")
	assert.Equal(t, before, after, "sections other than plugins must be untouched")
}

func rawConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))
	return doc
}

func TestAddDefaultsToDisabledAndFindsPackageByName(t *testing.T) {
	e := setup(t)
	writePackage(t, filepath.Join(e.workspace, "plugins"), "found", nil)

	_, err := execute([]string{"add", "found"}, "")
	require.NoError(t, err)
	cfg := e.load(t)
	assert.False(t, cfg.Plugins.Entries["found"].Enabled)
	assert.False(t, cfg.Plugins.Enabled, "a disabled entry must not switch the host on")
}

func TestAddRejectsBadInputWithoutSaving(t *testing.T) {
	e := setup(t)
	t.Chdir(t.TempDir())
	require.NoError(t, os.MkdirAll("broken", 0o755))
	require.NoError(t, os.WriteFile("broken/plugin.json", []byte(`{"name":"broken"}`), 0o644))

	for _, args := range [][]string{
		{"add", "Bad Id"},
		{"add", "ghost"},
		{"add", "broken", "--path", "./broken"},
		{"add", "missing", "--path", "./nowhere"},
	} {
		_, err := execute(args, "")
		assert.Error(t, err, args)
	}
	assert.Empty(t, e.load(t).Plugins.Entries)
}

func TestAddExistingEntryMergesAfterConfirmation(t *testing.T) {
	e := setup(t)
	root := writePackage(t, t.TempDir(), "mem", nil)
	e.save(t, func(p *config.PluginsConfig) {
		p.Entries = map[string]config.PluginEntryConfig{
			"mem": {AllowHooks: true, Config: map[string]string{"k": "v"}, Agents: []string{"main"}},
		}
	})

	_, err := execute([]string{"add", "mem", "--path", root, "--enable"}, "n\n")
	require.Error(t, err)
	assert.False(t, e.load(t).Plugins.Entries["mem"].Enabled)

	_, err = execute([]string{"add", "mem", "--path", root, "--enable"}, "y\n")
	require.NoError(t, err)
	entry := e.load(t).Plugins.Entries["mem"]
	assert.True(t, entry.Enabled)
	assert.True(t, entry.AllowHooks)
	assert.Equal(t, map[string]string{"k": "v"}, entry.Config)
	assert.Equal(t, []string{"main"}, entry.Agents)

	_, err = execute([]string{"add", "mem", "--force"}, "")
	require.NoError(t, err)
	assert.True(t, e.load(t).Plugins.Entries["mem"].Enabled, "omitting --enable must not disable an entry")
}

func TestRemoveDisablesHostWithLastEntry(t *testing.T) {
	e := setup(t)
	e.save(t, func(p *config.PluginsConfig) {
		p.Enabled = true
		p.Entries = map[string]config.PluginEntryConfig{"a": {Enabled: true}, "b": {Enabled: true}}
	})

	_, err := execute([]string{"remove", "a"}, "")
	require.NoError(t, err)
	got := e.load(t).Plugins
	assert.True(t, got.Enabled)
	assert.Len(t, got.Entries, 1)

	_, err = execute([]string{"remove", "b"}, "")
	require.NoError(t, err)
	got = e.load(t).Plugins
	assert.False(t, got.Enabled)
	assert.Empty(t, got.Entries)

	_, err = execute([]string{"remove", "b"}, "")
	assert.ErrorContains(t, err, "not found")
}

func TestEnableDisable(t *testing.T) {
	e := setup(t)
	e.save(t, func(p *config.PluginsConfig) {
		p.Entries = map[string]config.PluginEntryConfig{"a": {AllowHooks: true}}
	})

	_, err := execute([]string{"enable", "a"}, "")
	require.NoError(t, err)
	got := e.load(t).Plugins
	assert.True(t, got.Enabled)
	assert.True(t, got.Entries["a"].Enabled)
	assert.True(t, got.Entries["a"].AllowHooks)

	_, err = execute([]string{"disable", "a"}, "")
	require.NoError(t, err)
	assert.False(t, e.load(t).Plugins.Entries["a"].Enabled)

	for _, verb := range []string{"enable", "disable"} {
		_, err = execute([]string{verb, "ghost"}, "")
		assert.ErrorContains(t, err, "not found", verb)
	}
}

func TestValidate(t *testing.T) {
	e := setup(t)
	dir := t.TempDir()
	good := writePackage(t, dir, "good", nil)
	typo := writePackage(t, dir, "typo", map[string]any{"tools": true})
	bad := filepath.Join(dir, "bad")
	require.NoError(t, os.MkdirAll(bad, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bad, "plugin.json"), []byte(`{"name":"bad"}`), 0o644))

	out, err := execute([]string{"validate", good}, "")
	require.NoError(t, err)
	assert.Contains(t, out, "✓ good: 1 skills")

	out, err = execute([]string{"validate", typo}, "")
	require.NoError(t, err, "warnings do not fail by default")
	assert.Contains(t, out, `ignored unknown field "tools"`)
	_, err = execute([]string{"validate", typo, "--strict"}, "")
	assert.ErrorContains(t, err, "--strict")

	out, err = execute([]string{"validate", bad}, "")
	assert.ErrorContains(t, err, "$schema")
	assert.Contains(t, out, "Validating")

	// An id resolves through the configured entry, so its overrides count.
	e.save(t, func(p *config.PluginsConfig) {
		p.Entries = map[string]config.PluginEntryConfig{"alias": {Path: good}}
	})
	out, err = execute([]string{"validate", "alias"}, "")
	require.NoError(t, err)
	assert.Contains(t, out, "✓ good:")
}

func TestValidateFlagsHookThatTheHostWouldReject(t *testing.T) {
	setup(t)
	// observe + append_to_user_message is not accepted: the result is only valid on before_llm intercepts.
	hooks := map[string]any{"extensions": map[string]any{plugins.ExtensionNamespace: map[string]any{"hooks": []any{
		map[string]any{
			"name": "recall", "observe": []string{"before_llm"},
			"mcp": map[string]any{"server": "s", "tool": "t", "result": "append_to_user_message"},
		},
	}}}}
	root := writePackage(t, t.TempDir(), "hooked", hooks)

	out, err := execute([]string{"validate", root}, "")
	require.NoError(t, err)
	assert.Contains(t, out, "can append results only on before_llm")
	_, err = execute([]string{"validate", root, "--strict"}, "")
	assert.Error(t, err)
}

func TestInitProducesValidPackage(t *testing.T) {
	setup(t)
	dir := t.TempDir()
	for _, name := range []string{"weather", "my.plugin-v2", "a.-b"} {
		_, err := execute([]string{"init", name, "--dir", dir}, "")
		require.NoError(t, err, name)
		out, err := execute([]string{"validate", filepath.Join(dir, name), "--strict"}, "")
		require.NoError(t, err, "%s: %s", name, out)
		assert.Contains(t, out, "1 skills")
		assert.FileExists(t, filepath.Join(dir, name, "README.md"))
	}

	_, err := execute([]string{"init", "weather", "--dir", dir}, "")
	assert.ErrorContains(t, err, "already exists", "init must not overwrite a package")
	_, err = execute([]string{"init", "Not Valid", "--dir", dir}, "")
	assert.ErrorContains(t, err, "invalid plugin name")
}

func TestInitDoesNotTouchConfig(t *testing.T) {
	e := setup(t)
	before, err := os.ReadFile(e.configPath)
	require.NoError(t, err)
	_, err = execute([]string{"init", "weather", "--dir", t.TempDir()}, "")
	require.NoError(t, err)
	after, err := os.ReadFile(e.configPath)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestShow(t *testing.T) {
	e := setup(t)
	root := writePackage(t, t.TempDir(), "demo", map[string]any{
		"version": "1.2.3", "description": "Demo plugin", "author": map[string]any{"name": "Ada"},
	})
	e.save(t, func(p *config.PluginsConfig) {
		p.Entries = map[string]config.PluginEntryConfig{"demo": {Path: root}}
	})

	out, err := execute([]string{"show", "demo"}, "")
	require.NoError(t, err)
	for _, want := range []string{"Version:     1.2.3", "Description: Demo plugin", "Author:      Ada", "demo:greet", "disabled"} {
		assert.Contains(t, out, want)
	}

	_, err = execute([]string{"show", "ghost"}, "")
	assert.ErrorContains(t, err, `plugin "ghost" not found`)
}

func TestTestCommand(t *testing.T) {
	e := setup(t)
	dir := t.TempDir()
	good := writePackage(t, dir, "good", nil)
	broken := filepath.Join(dir, "broken")
	require.NoError(t, os.MkdirAll(broken, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(broken, "plugin.json"), []byte(`{"name":"broken"}`), 0o644))
	e.save(t, func(p *config.PluginsConfig) {
		p.DataDir = t.TempDir()
		p.Entries = map[string]config.PluginEntryConfig{"good": {Path: good}, "broken": {Path: broken}}
	})

	// The entry is disabled and the host switch is off; test loads it anyway.
	out, err := execute([]string{"test", "good"}, "")
	require.NoError(t, err)
	assert.Contains(t, out, "Live state:  ready")
	assert.Contains(t, out, "good:greet")
	assert.False(t, e.load(t).Plugins.Entries["good"].Enabled, "test must not change the config")

	out, err = execute([]string{"test", "broken"}, "")
	assert.ErrorContains(t, err, "failed to load")
	assert.Contains(t, out, "$schema")

	_, err = execute([]string{"test", "ghost"}, "")
	assert.Error(t, err)
}

func TestEditUsesEditor(t *testing.T) {
	e := setup(t)
	original := editorCommand
	defer func() { editorCommand = original }()
	var name string
	var args []string
	editorCommand = func(n string, a ...string) *exec.Cmd {
		name, args = n, append([]string(nil), a...)
		return exec.Command("sh", "-c", "exit 0")
	}

	t.Setenv("EDITOR", "dummy-editor --wait")
	_, err := execute([]string{"edit"}, "")
	require.NoError(t, err)
	assert.Equal(t, "dummy-editor", name)
	assert.Equal(t, []string{"--wait", e.configPath}, args)

	t.Setenv("EDITOR", "")
	_, err = execute([]string{"edit"}, "")
	assert.ErrorContains(t, err, "$EDITOR is not set")
}
