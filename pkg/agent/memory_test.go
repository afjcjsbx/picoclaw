package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/tools"
)

func TestFitMemory(t *testing.T) {
	t.Run("under the limit is untouched", func(t *testing.T) {
		if got := fitMemory("a\nb\n", 100, "x.md", "/w/x.md"); got != "a\nb" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("keeps headings and the newest sections", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString("# Memory\n\n## Old thing\n" + strings.Repeat("old detail line\n", 40))
		sb.WriteString("## Newest thing\nfresh fact one\nfresh fact two\n")
		got := fitMemory(sb.String(), 200, "memory/MEMORY.md", "/w/memory/MEMORY.md")

		for _, want := range []string{
			"# Memory", "## Old thing", "## Newest thing", "fresh fact two",
			"memory/MEMORY.md truncated", "read_file /w/memory/MEMORY.md",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in:\n%s", want, got)
			}
		}
		if strings.Contains(got, "old detail line") {
			t.Errorf("older section body should collapse to its heading:\n%s", got)
		}
		body, _, _ := strings.Cut(got, "\n\n[")
		if n := utf8.RuneCountInString(body); n > 200 {
			t.Errorf("body is %d chars, limit 200", n)
		}
	})

	t.Run("headingless text keeps the newest lines", func(t *testing.T) {
		got := fitMemory("first\nsecond\nthird\nfourth", 14, "x.md", "/w/x.md")
		if strings.Contains(got, "first") || !strings.Contains(got, "fourth") {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("one oversized line is cut from its start", func(t *testing.T) {
		got := fitMemory(strings.Repeat("a", 50)+"END", 10, "x.md", "/w/x.md")
		if !strings.Contains(got, "aaaaaaaEND") {
			t.Fatalf("got %q", got)
		}
	})
}

func TestMemorySnapshotRespectsConfig(t *testing.T) {
	ws := setupWorkspace(t, map[string]string{
		"memory/MEMORY.md": "- likes Go",
		"USER.md":          "Name: Ada",
	})
	defer os.RemoveAll(ws)
	cb := NewContextBuilder(ws)
	note := cb.memory.dailyNotePath(time.Now())
	if err := os.MkdirAll(filepath.Dir(note), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		note,
		[]byte("## 10:00\n"+strings.Repeat("noise\n", 300)+"## 11:00\nlast entry"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	got := cb.memory.Snapshot()
	for _, want := range []string{"likes Go", "Name: Ada", "last entry", "## 10:00", "truncated"} {
		if !strings.Contains(got, want) {
			t.Errorf("snapshot missing %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "noise"); n != 0 {
		t.Errorf(
			"daily note should be bounded to %d chars, still has %d noise lines",
			config.DefaultDailyNotesCharLimit,
			n,
		)
	}

	cb.WithMemoryConfig(config.MemoryConfig{DailyNotesEnabled: false}) // memory_enabled=false
	got = cb.memory.Snapshot()
	if strings.Contains(got, "likes Go") || strings.Contains(got, "last entry") || !strings.Contains(got, "Name: Ada") {
		t.Errorf("memory and daily notes off should leave only USER.md, got:\n%s", got)
	}
}

// A 60 KB MEMORY.md (issue #439) must not reach the prompt in full, and the
// model must be told what was left out.
func TestOversizedMemoryIsBoundedInPrompt(t *testing.T) {
	var sb strings.Builder
	for i := range 60 {
		sb.WriteString(
			"## Investigation " + string(rune('A'+i%26)) + "\n" + strings.Repeat("closed PR notes ", 60) + "\n",
		)
	}
	ws := setupWorkspace(t, map[string]string{"memory/MEMORY.md": sb.String()})
	defer os.RemoveAll(ws)

	cb := NewContextBuilder(ws)
	system := cb.BuildMessagesFromPrompt(PromptBuildRequest{SessionKey: "s", CurrentMessage: "hi"})[0].Content
	if len(system) > 12_000 {
		t.Fatalf("system prompt is %d bytes, memory was not bounded", len(system))
	}
	if !strings.Contains(system, "memory/MEMORY.md truncated") {
		t.Fatal("prompt must say memory was truncated")
	}
}

// Hermes-style snapshot: a running session keeps the memory it started with.
func TestMemorySnapshotIsFrozenPerSession(t *testing.T) {
	ws := setupWorkspace(t, map[string]string{"memory/MEMORY.md": "fact v1"})
	defer os.RemoveAll(ws)
	cb := NewContextBuilder(ws)
	system := func(session string) string {
		return cb.BuildMessagesFromPrompt(PromptBuildRequest{SessionKey: session, CurrentMessage: "hi"})[0].Content
	}

	first := system("a")
	if err := os.WriteFile(filepath.Join(ws, "memory", "MEMORY.md"), []byte("fact v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if again := system("a"); again != first {
		t.Fatal("system prompt of a running session must not change when memory is written")
	}
	if !strings.Contains(system("b"), "fact v2") {
		t.Fatal("a new session must see the new memory")
	}
	if !strings.Contains(cb.BuildMessagesFromPrompt(PromptBuildRequest{CurrentMessage: "hi"})[0].Content, "fact v2") {
		t.Fatal("a request without a session key reads memory live")
	}
}

// End to end: a memory tool write lands in the file at once, stays out of the
// running session's prompt and cached static prompt, and reaches the next one.
func TestMemoryToolWriteReachesNextSessionOnly(t *testing.T) {
	ws := setupWorkspace(t, nil)
	defer os.RemoveAll(ws)
	cb := NewContextBuilder(ws)
	system := func(session string) string {
		return cb.BuildMessagesFromPrompt(PromptBuildRequest{SessionKey: session, CurrentMessage: "hi"})[0].Content
	}
	before := system("a")

	res := tools.NewMemoryTool(ws, 2200, 1375).Execute(context.Background(), map[string]any{
		"action": "add", "target": "memory", "content": "Deploys go through staging first",
	})
	if res.IsError {
		t.Fatalf("memory write failed: %s", res.ForLLM)
	}

	if after := system("a"); after != before {
		t.Fatal("the running session's system prompt changed after a memory write")
	}
	if !strings.Contains(system("b"), "Deploys go through staging first") {
		t.Fatal("the next session must see the saved memory")
	}
}
