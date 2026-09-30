package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestDiscoveryRequiresActivationAndRejectsAmbiguity(t *testing.T) {
	workspace := t.TempDir()
	for _, folder := range []string{"one", "two"} {
		writeTestFile(
			t,
			workspace,
			filepath.Join("plugins", folder, "plugin.json"),
			manifestJSON(map[string]any{"name": "demo"}),
		)
	}
	cfg := config.PluginsConfig{Enabled: true, Directories: []string{"plugins"}, DataDir: t.TempDir()}
	if items, _ := discover(cfg, workspace); len(items) != 0 {
		t.Fatal("discovery activated an unconfigured plugin")
	}
	cfg.Entries = map[string]config.PluginEntryConfig{"demo": {Enabled: true}}
	items, _ := discover(cfg, workspace)
	if len(items) != 1 || items[0].err == nil {
		t.Fatalf("ambiguous name accepted: %+v", items)
	}
	cfg.Entries["demo"] = config.PluginEntryConfig{Enabled: true, Path: "plugins/one"}
	items, _ = discover(cfg, workspace)
	if len(items) != 1 || items[0].err != nil {
		t.Fatalf("explicit path failed: %+v", items)
	}
	cfg.Entries["other"] = config.PluginEntryConfig{Enabled: true, Path: "plugins/one"}
	items, _ = discover(cfg, workspace)
	if len(items) != 2 || items[1].err == nil {
		t.Fatalf("duplicate root accepted: %+v", items)
	}
}

func TestComponentPathFailuresPreserveOtherCapabilities(t *testing.T) {
	root, _ := canonicalRoot(t.TempDir())
	writeTestFile(t, root, "plugin.json", manifestJSON(nil))
	writeTestFile(t, root, "skills/good/SKILL.md", []byte("---\nname: good\ndescription: Good skill\n---\nGood."))
	writeTestFile(t, root, "skills/bad/SKILL.md", []byte("---\nname: other\ndescription: Invalid\n---\nBad."))
	writeTestFile(
		t,
		root,
		"skills/nested/deeper/SKILL.md",
		[]byte("---\nname: deeper\ndescription: Not discovered\n---\nNested."),
	)
	if err := os.Mkdir(filepath.Join(root, "mcp.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	count := 0
	m := NewManager(
		testConfig(root, t.TempDir()),
		root,
		func(_ context.Context, capabilities Capabilities) []Diagnostic {
			count += len(capabilities.Skills)
			return nil
		},
	)
	defer m.Close()
	if err := m.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count != 1 || m.Statuses()[0].State != Degraded {
		t.Fatalf("count=%d status=%+v", count, m.Statuses())
	}
}

func TestPluginLoadsIndependentlyOfHungSibling(t *testing.T) {
	hung := fixturePlugin(t)
	writeServers(t, hung, map[string]any{"hung": serverFixture(map[string]string{"PLUGIN_TEST_HANG": "1"})})
	healthy := t.TempDir()
	writeTestFile(t, healthy, "plugin.json", manifestJSON(nil))
	writeTestFile(t, healthy, "skills/greet/SKILL.md", []byte("---\nname: greet\ndescription: Hello\n---\nHi."))
	cfg := testConfig(hung, t.TempDir())
	cfg.InitTimeoutMS = 3000
	cfg.Concurrency = 2
	cfg.Entries = map[string]config.PluginEntryConfig{
		"a-hung":    {Enabled: true, Path: hung},
		"b-healthy": {Enabled: true, Path: healthy},
	}
	ready := make(chan struct{}, 1)
	m := NewManager(cfg, hung, func(_ context.Context, capabilities Capabilities) []Diagnostic {
		if capabilities.ID == "b-healthy" {
			ready <- struct{}{}
		}
		return nil
	})
	defer m.Close()
	m.Start()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("healthy plugin blocked behind hung sibling")
	}
}

func TestHookExtensionValidationAndOptIn(t *testing.T) {
	root := fixturePlugin(t)
	raw, _ := json.Marshal(
		map[string]any{
			"hooks": []any{
				map[string]any{"name": "good", "command": "./helper.exe", "intercept": []string{"after_llm"}},
				map[string]any{"name": "bad", "command": "../escape"},
			},
		},
	)
	manifest := PluginManifest{Extensions: map[string]json.RawMessage{ExtensionNamespace: raw}}
	pc := PluginContext{ID: "demo", Root: root, DataDir: root}
	if hooks, diagnostics := discoverHooks(manifest, pc); len(hooks) != 0 || len(diagnostics) != 0 {
		t.Fatal("disabled hooks were processed")
	}
	pc.AllowHooks = true
	hooks, diagnostics := discoverHooks(manifest, pc)
	if len(hooks) != 1 || len(diagnostics) != 1 {
		t.Fatalf("hooks=%+v diagnostics=%+v", hooks, diagnostics)
	}
	writeTestFile(t, root, filepath.Join(ExtensionNamespace, "hooks.json"), raw)
	if hooks, diagnostics := discoverHooks(manifest, pc); len(hooks) != 0 || len(diagnostics) != 1 {
		t.Fatal("ambiguous extension accepted")
	}
	manifest.Extensions = nil
	if hooks, diagnostics := discoverHooks(manifest, pc); len(hooks) != 1 || len(diagnostics) != 1 {
		t.Fatalf("file extension: hooks=%v diagnostics=%v", hooks, diagnostics)
	}
}

func TestUnavailableDataPreservesSkills(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "plugin.json", manifestJSON(nil))
	writeTestFile(t, root, "skills/greet/SKILL.md", []byte("---\nname: greet\ndescription: Hello\n---\nHi."))
	writeTestFile(t, root, "not-a-directory", []byte("file"))
	cfg := testConfig(root, filepath.Join(root, "not-a-directory"))
	count := 0
	m := NewManager(
		cfg,
		root,
		func(_ context.Context, capabilities Capabilities) []Diagnostic {
			count += len(capabilities.Skills)
			return nil
		},
	)
	defer m.Close()
	if err := m.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if count != 1 || m.Statuses()[0].State != Degraded {
		t.Fatalf("count=%d status=%+v", count, m.Statuses())
	}
}
