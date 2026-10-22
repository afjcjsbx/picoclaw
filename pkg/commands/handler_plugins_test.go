package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestListPlugins_FormatsInventory(t *testing.T) {
	rt := &Runtime{
		Config: &config.Config{Plugins: config.PluginsConfig{Enabled: true}},
		ListPlugins: func(_ context.Context) []PluginInfo {
			return []PluginInfo{
				{ID: "demo-plugin", Name: "demo-plugin", Root: "/plugins/demo", State: "ready"},
				{ID: "alias", Name: "other", Root: "/plugins/other", State: "degraded", Diagnostics: 2},
				{ID: "beta", Root: "/plugins/beta", State: "not enabled"},
			}
		},
	}

	var reply string
	res := NewExecutor(NewRegistry(BuiltinDefinitions()), rt).Execute(context.Background(), Request{
		Text: "/list plugins",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/list plugins outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}

	want := "Installed Plugins:\n" +
		"- `demo-plugin`\n  State: ready\n  Path: /plugins/demo\n" +
		"\n" +
		"- `alias`\n  Name: other\n  State: degraded\n  Path: /plugins/other\n  Diagnostics: 2\n" +
		"\n" +
		"- `beta`\n  State: not enabled\n  Path: /plugins/beta"
	if reply != want {
		t.Fatalf("/list plugins reply=%q, want=%q", reply, want)
	}
}

func TestListPlugins_HostDisabledAndEmpty(t *testing.T) {
	rt := &Runtime{
		Config: &config.Config{Plugins: config.PluginsConfig{Enabled: false}},
		ListPlugins: func(_ context.Context) []PluginInfo {
			return []PluginInfo{{ID: "demo", Root: "/plugins/demo", State: "disabled"}}
		},
	}
	var reply string
	req := func() Request {
		return Request{
			Text: "/list plugins",
			Reply: func(text string) error {
				reply = text
				return nil
			},
		}
	}
	NewExecutor(NewRegistry(BuiltinDefinitions()), rt).Execute(context.Background(), req())
	if !strings.HasPrefix(reply, "Installed Plugins (plugin host disabled):\n") {
		t.Fatalf("disabled host reply=%q, want host-disabled header", reply)
	}

	rt.ListPlugins = func(_ context.Context) []PluginInfo { return nil }
	NewExecutor(NewRegistry(BuiltinDefinitions()), rt).Execute(context.Background(), req())
	if reply != "No plugins installed" {
		t.Fatalf("empty reply=%q, want %q", reply, "No plugins installed")
	}
}

func TestListPlugins_UnavailableWithoutRuntimeHook(t *testing.T) {
	var reply string
	NewExecutor(NewRegistry(BuiltinDefinitions()), &Runtime{}).Execute(context.Background(), Request{
		Text: "/list plugins",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if reply != unavailableMsg {
		t.Fatalf("reply=%q, want %q", reply, unavailableMsg)
	}
}
