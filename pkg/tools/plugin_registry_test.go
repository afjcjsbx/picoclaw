package tools

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

type pluginRegistryTool struct{ value string }

func (*pluginRegistryTool) Name() string               { return "plugin_collision" }
func (*pluginRegistryTool) Description() string        { return "test" }
func (*pluginRegistryTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (t *pluginRegistryTool) Execute(context.Context, map[string]any) *ToolResult {
	return NewToolResult(t.value)
}

func TestPluginRegistrationOwnershipAndConcurrency(t *testing.T) {
	r := NewToolRegistry()
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			added, _ := r.RegisterUnique(&pluginRegistryTool{value: "original"}, false)
			if added {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("%d registrations", successes.Load())
	}
	original, _ := r.Get("plugin_collision")
	r.UnregisterOwned(&pluginRegistryTool{value: "other"})
	if !r.HasRegistered("plugin_collision") {
		t.Fatal("foreign owner removed tool")
	}
	r.UnregisterOwned(original)
	if r.HasRegistered("plugin_collision") {
		t.Fatal("owner could not remove tool")
	}
	r.SetAllowlist([]string{})
	if added, err := r.RegisterUnique(&pluginRegistryTool{}, false); err != nil || added {
		t.Fatalf("allowlist bypass: added=%v err=%v", added, err)
	}
}
