package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runMemory(t *testing.T, tool *MemoryTool, args map[string]any) (map[string]any, *ToolResult) {
	t.Helper()
	res := tool.Execute(context.Background(), args)
	var out map[string]any
	if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil {
		out = nil // plain-text error
	}
	return out, res
}

func TestMemoryTool(t *testing.T) {
	ws := t.TempDir()
	tool := NewMemoryTool(ws, 60, 40)
	file := filepath.Join(ws, "memory", "MEMORY.md")
	add := func(c string) (map[string]any, *ToolResult) {
		return runMemory(t, tool, map[string]any{"action": "add", "target": "memory", "content": c})
	}

	if out, res := add("Uses Go 1.25"); res.IsError || out["success"] != true {
		t.Fatalf("add failed: %s", res.ForLLM)
	}
	add("Uses Go 1.25") // duplicate line is a no-op
	add("Prefers tabs")
	if got, _ := os.ReadFile(file); string(got) != "Uses Go 1.25\nPrefers tabs\n" {
		t.Fatalf("file = %q", got)
	}

	// Over the limit: refuse, report chars to free, list entries, leave file alone.
	out, res := add("A long fact that does not fit in the remaining space of the file")
	if !res.IsError || out["success"] != false {
		t.Fatalf("expected refusal, got %s", res.ForLLM)
	}
	if free, _ := out["chars_to_free"].(float64); free <= 0 {
		t.Errorf("chars_to_free = %v", out["chars_to_free"])
	}
	if entries, _ := out["current_entries"].([]any); len(entries) != 2 {
		t.Errorf("current_entries = %v", out["current_entries"])
	}
	if got, _ := os.ReadFile(file); string(got) != "Uses Go 1.25\nPrefers tabs\n" {
		t.Fatalf("refused write changed the file: %q", got)
	}

	// Ambiguous and missing old_text are rejected.
	for _, old := range []string{"s", "nope"} {
		if _, res := runMemory(t, tool, map[string]any{
			"action": "replace", "target": "memory", "old_text": old, "content": "x",
		}); !res.IsError {
			t.Errorf("old_text %q should fail", old)
		}
	}

	// Replace shortens, remove drops the whole line, then the long fact fits.
	runMemory(t, tool, map[string]any{
		"action": "replace", "target": "memory", "old_text": "Uses Go 1.25", "content": "Go",
	})
	runMemory(t, tool, map[string]any{"action": "remove", "target": "memory", "old_text": "tabs"})
	if got, _ := os.ReadFile(file); string(got) != "Go\n" {
		t.Fatalf("file = %q", got)
	}
	if out, res := add("A long fact that fits now"); res.IsError || out["success"] != true {
		t.Fatalf("add after freeing failed: %s", res.ForLLM)
	}
}

func TestMemoryToolShrinksOversizedFile(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(ws, "USER.md"),
		[]byte(strings.Repeat("x", 100)+"\nkeep\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	tool := NewMemoryTool(ws, 60, 40)
	// Still over the limit after the edit, but smaller: allowed.
	_, res := runMemory(t, tool, map[string]any{
		"action": "replace", "target": "user", "old_text": strings.Repeat("x", 100), "content": strings.Repeat("x", 50),
	})
	if res.IsError {
		t.Fatalf("shrinking edit refused: %s", res.ForLLM)
	}
	// Growing is not.
	if _, res := runMemory(
		t,
		tool,
		map[string]any{"action": "add", "target": "user", "content": "more"},
	); !res.IsError {
		t.Fatal("add to an oversized file should fail")
	}
}
