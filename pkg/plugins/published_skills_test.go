package plugins

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
)

func writeSkillPackage(t *testing.T, workspace, id string, skills ...string) string {
	t.Helper()
	writeTestFile(t, workspace, "plugins/"+id+"/plugin.json", manifestJSON(map[string]any{"name": id}))
	root, err := canonicalRoot(filepath.Join(workspace, "plugins", id))
	require.NoError(t, err)
	for _, name := range skills {
		writeTestFile(
			t,
			workspace,
			"plugins/"+id+"/skills/"+name+"/SKILL.md",
			[]byte("---\nname: "+name+"\ndescription: "+name+" skill\n---\n"+name+" body\n"),
		)
	}
	return root
}

func TestPublishedSkillsRequiresActivation(t *testing.T) {
	workspace := t.TempDir()
	writeSkillPackage(t, workspace, "demo", "greet")

	entry := map[string]config.PluginEntryConfig{"demo": {Enabled: true}}
	for _, cfg := range []config.PluginsConfig{
		{Enabled: false, Directories: []string{"plugins"}, Entries: entry},
		{Enabled: true, Directories: []string{"plugins"}, Entries: map[string]config.PluginEntryConfig{
			"demo": {Enabled: false},
		}},
	} {
		packages, _ := PublishedSkills(cfg, workspace)
		require.Empty(t, packages)
	}
}

func TestPublishedSkillsListsEnabledSkills(t *testing.T) {
	workspace := t.TempDir()
	root := writeSkillPackage(t, workspace, "demo", "greet", "other")

	packages, diagnostics := PublishedSkills(config.PluginsConfig{
		Enabled:     true,
		Directories: []string{"plugins"},
		Entries:     map[string]config.PluginEntryConfig{"demo": {Enabled: true}},
	}, workspace)

	require.Empty(t, diagnostics)
	require.Len(t, packages, 1)
	require.Equal(t, "demo", packages[0].ID)
	require.Equal(t, "demo", packages[0].Name)
	require.Equal(t, root, packages[0].Root)
	require.Len(t, packages[0].Skills, 2)
	require.Equal(t, "demo:greet", packages[0].Skills[0].Info.Name)
	require.Equal(t, "greet body\n", packages[0].Skills[0].Body)
	require.Equal(t, "demo:other", packages[0].Skills[1].Info.Name)
}

func TestPublishedSkillsAppliesSkillNames(t *testing.T) {
	workspace := t.TempDir()
	writeSkillPackage(t, workspace, "demo", "greet", "other")

	packages, _ := PublishedSkills(config.PluginsConfig{
		Enabled:     true,
		Directories: []string{"plugins"},
		Entries: map[string]config.PluginEntryConfig{
			"demo": {Enabled: true, SkillNames: []string{"other"}},
		},
	}, workspace)

	require.Len(t, packages, 1)
	require.Len(t, packages[0].Skills, 1)
	require.Equal(t, "demo:other", packages[0].Skills[0].Info.Name)
}

func TestPublishedSkillsSkipsInvalidManifest(t *testing.T) {
	workspace := t.TempDir()
	writeTestFile(t, workspace, "plugins/demo/plugin.json", []byte("{not json"))
	writeTestFile(
		t,
		workspace,
		"plugins/demo/skills/greet/SKILL.md",
		[]byte("---\nname: greet\ndescription: Greet skill\n---\nHi.\n"),
	)

	packages, diagnostics := PublishedSkills(config.PluginsConfig{
		Enabled:     true,
		Directories: []string{"plugins"},
		Entries:     map[string]config.PluginEntryConfig{"demo": {Enabled: true}},
	}, workspace)

	require.Empty(t, packages)
	require.NotEmpty(t, diagnostics)
}
