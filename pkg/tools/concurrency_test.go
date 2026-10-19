package tools

import "testing"

type concurrencySafeRegistryTool struct {
	mockRegistryTool
	safe bool
}

func (m *concurrencySafeRegistryTool) IsConcurrencySafe() bool { return m.safe }

type concurrencySafeAsyncRegistryTool struct {
	mockAsyncRegistryTool
}

func (m *concurrencySafeAsyncRegistryTool) IsConcurrencySafe() bool { return true }

func TestIsConcurrencySafe(t *testing.T) {
	safe := &concurrencySafeRegistryTool{
		mockRegistryTool: *newMockTool("safe", "safe tool"),
		safe:             true,
	}
	unsafe := &concurrencySafeRegistryTool{
		mockRegistryTool: *newMockTool("unsafe", "unsafe tool"),
		safe:             false,
	}
	asyncSafe := &concurrencySafeAsyncRegistryTool{
		mockAsyncRegistryTool: mockAsyncRegistryTool{
			mockRegistryTool: *newMockTool("async_safe", "async safe tool"),
		},
	}
	plain := newMockTool("plain", "plain tool")

	tests := []struct {
		name string
		tool Tool
		want bool
	}{
		{name: "safe", tool: safe, want: true},
		{name: "explicitly unsafe", tool: unsafe, want: false},
		{name: "async is never concurrent safe", tool: asyncSafe, want: false},
		{name: "no marker", tool: plain, want: false},
		{name: "nil", tool: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsConcurrencySafe(tt.tool); got != tt.want {
				t.Fatalf("IsConcurrencySafe() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToolRegistry_IsConcurrencySafe(t *testing.T) {
	registry := NewToolRegistry()
	safe := &concurrencySafeRegistryTool{
		mockRegistryTool: *newMockTool("safe", "safe tool"),
		safe:             true,
	}
	plain := newMockTool("plain", "plain tool")
	hidden := &concurrencySafeRegistryTool{
		mockRegistryTool: *newMockTool("hidden_safe", "hidden safe tool"),
		safe:             true,
	}
	registry.Register(safe)
	registry.Register(plain)
	registry.RegisterHidden(hidden)

	if !registry.IsConcurrencySafe("safe") {
		t.Fatal("registered safe tool should be concurrency-safe")
	}
	if registry.IsConcurrencySafe("plain") {
		t.Fatal("tool without the marker should not be concurrency-safe")
	}
	if registry.IsConcurrencySafe("missing") {
		t.Fatal("unregistered tool should not be concurrency-safe")
	}
	if registry.IsConcurrencySafe("hidden_safe") {
		t.Fatal("hidden tool with zero TTL should not be concurrency-safe")
	}

	registry.PromoteTools([]string{"hidden_safe"}, 1)
	if !registry.IsConcurrencySafe("hidden_safe") {
		t.Fatal("promoted safe tool should be concurrency-safe")
	}
}
