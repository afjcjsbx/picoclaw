package plugins

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
)

func writePackageAt(t *testing.T, dir string, manifest []byte) string {
	t.Helper()
	writeTestFile(t, dir, "plugin.json", manifest)
	root, err := canonicalRoot(dir)
	require.NoError(t, err)
	return root
}

func TestInventoryCombinesEntriesPackagesAndStatuses(t *testing.T) {
	base, err := canonicalRoot(t.TempDir())
	require.NoError(t, err)
	pluginsDir := filepath.Join(base, "plugins")
	alphaRoot := writePackageAt(t, filepath.Join(pluginsDir, "alpha"), manifestJSON(map[string]any{"name": "alpha"}))
	betaRoot := writePackageAt(t, filepath.Join(pluginsDir, "beta"), manifestJSON(map[string]any{"name": "beta"}))
	brokenRoot := writePackageAt(t, filepath.Join(pluginsDir, "broken"), []byte("{not json"))
	gammaDir := t.TempDir()
	gammaRoot := writePackageAt(t, gammaDir, manifestJSON(map[string]any{"name": "gamma"}))

	cfg := config.PluginsConfig{
		Enabled:     true,
		Directories: []string{pluginsDir},
		Entries: map[string]config.PluginEntryConfig{
			"alpha": {Enabled: true},
			"delta": {Enabled: true},
			"gamma": {Enabled: false, Path: gammaDir},
		},
	}
	statuses := []Status{
		{ID: "alpha", Name: "alpha", Root: alphaRoot, State: Ready},
		{
			ID:    "delta",
			State: Failed,
			Diagnostics: []Diagnostic{
				{Plugin: "delta", Component: "discovery", Message: `plugin "delta" was not found`},
			},
		},
		{Name: "orphan", State: Failed},
	}

	require.Equal(t, []Installed{
		{ID: "alpha", Name: "alpha", Root: alphaRoot, State: "ready"},
		{ID: "beta", Root: betaRoot, State: "not enabled"},
		{ID: "broken", Root: brokenRoot, State: "not enabled"},
		{ID: "delta", State: "failed", Diagnostics: 1},
		{ID: "gamma", Root: gammaRoot, State: "disabled"},
	}, Inventory(cfg, base, statuses))
}

func TestInventoryStatesWithoutRuntimeStatuses(t *testing.T) {
	base, err := canonicalRoot(t.TempDir())
	require.NoError(t, err)
	pluginsDir := filepath.Join(base, "plugins")
	alphaRoot := writePackageAt(t, filepath.Join(pluginsDir, "alpha"), manifestJSON(map[string]any{"name": "alpha"}))

	entries := map[string]config.PluginEntryConfig{"alpha": {Enabled: true}}

	t.Run("host not started", func(t *testing.T) {
		cfg := config.PluginsConfig{Enabled: true, Directories: []string{pluginsDir}, Entries: entries}
		require.Equal(t, []Installed{{ID: "alpha", Root: alphaRoot, State: "pending"}}, Inventory(cfg, base, nil))
	})

	t.Run("plugin host disabled", func(t *testing.T) {
		cfg := config.PluginsConfig{Enabled: false, Directories: []string{pluginsDir}, Entries: entries}
		require.Equal(t, []Installed{{ID: "alpha", Root: alphaRoot, State: "disabled"}}, Inventory(cfg, base, nil))
	})
}
