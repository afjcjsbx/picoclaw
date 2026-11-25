package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLoadBootstrapFilesCapsPerFile(t *testing.T) {
	body := strings.Repeat("a", 5000)
	ws := setupWorkspace(t, map[string]string{
		"AGENT.md": "---\nname: pico\n---\n# Agent\n\n" + body,
	})
	defer cleanupWorkspace(t, ws)

	cb := NewContextBuilder(ws).WithBootstrapLimits(1000, 2400)
	bootstrap := cb.LoadBootstrapFiles()

	if strings.Contains(bootstrap, "name: pico") {
		t.Fatalf("bootstrap must not include frontmatter: %q", bootstrap)
	}
	if !strings.Contains(bootstrap, "bootstrap file truncated") {
		t.Fatalf("bootstrap must report truncation: %q", bootstrap)
	}
	if !strings.Contains(bootstrap, "read_file on AGENT.md") {
		t.Fatalf("shell notice must point at AGENT.md: %q", bootstrap)
	}
	if !strings.Contains(bootstrap, "of 5009 characters") {
		t.Fatalf("notice must report the original body size: %q", bootstrap)
	}

	section := strings.TrimPrefix(bootstrap, "## AGENT.md\n\n")
	section = strings.TrimSuffix(section, "\n\n")
	if n := utf8.RuneCountInString(section); n > 1000 {
		t.Fatalf("section is %d chars, want at most 1000", n)
	}
	if !utf8.ValidString(bootstrap) {
		t.Fatal("truncated bootstrap is not valid UTF-8")
	}
}

func TestLoadBootstrapFilesCapsTotal(t *testing.T) {
	ws := setupWorkspace(t, map[string]string{
		"AGENT.md":    strings.Repeat("a", 800),
		"SOUL.md":     strings.Repeat("b", 800),
		"IDENTITY.md": strings.Repeat("c", 800),
	})
	defer cleanupWorkspace(t, ws)

	cb := NewContextBuilder(ws).WithBootstrapLimits(1000, 1200)
	bootstrap := cb.LoadBootstrapFiles()

	if n := utf8.RuneCountInString(bootstrap); n > 1200 {
		t.Fatalf("bootstrap is %d chars, want at most 1200", n)
	}
	if !strings.Contains(bootstrap, "bootstrap file truncated") {
		t.Fatalf("bootstrap must report truncation: %q", bootstrap)
	}
	if strings.Contains(bootstrap, "## IDENTITY.md") {
		t.Fatalf("total budget should stop before IDENTITY.md: %q", bootstrap)
	}
}

func TestLoadBootstrapFilesExactLimitIsUntouched(t *testing.T) {
	body := strings.Repeat("x", 1000)
	ws := setupWorkspace(t, map[string]string{"AGENT.md": body})
	defer cleanupWorkspace(t, ws)

	bootstrap := NewContextBuilder(ws).
		WithBootstrapLimits(1000, 2400).
		LoadBootstrapFiles()

	if !strings.Contains(bootstrap, body) {
		t.Fatalf("content at the exact limit must be preserved")
	}
	if strings.Contains(bootstrap, "bootstrap file truncated") {
		t.Fatalf("content at the exact limit must not be truncated: %q", bootstrap)
	}
}

func TestTruncateBootstrapTextNeverSplitsUTF8(t *testing.T) {
	text := strings.Repeat("a", 200) + "😀" + strings.Repeat("b", 400)
	for _, limit := range []int{1, 50, 200, 201, 202, 250, 300} {
		got, truncated := truncateBootstrapText(text, limit, "AGENT.md")
		if !truncated {
			t.Fatalf("limit %d: expected truncation", limit)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("limit %d: result is not valid UTF-8: %q", limit, got)
		}
		if n := utf8.RuneCountInString(got); n > limit {
			t.Fatalf("limit %d: result has %d chars", limit, n)
		}
	}

	if got := bootstrapRunePrefix("a😀b", 1); got != "a" {
		t.Fatalf("prefix before emoji = %q", got)
	}
	if got := bootstrapRunePrefix("a😀b", 2); got != "a😀" {
		t.Fatalf("prefix including emoji = %q", got)
	}
}

func TestReadBootstrapFileCapsDiskRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENT.md")
	// One byte short of the cap followed by a four-byte rune: the byte cap
	// falls inside the emoji, so the reader must trim the partial sequence.
	data := strings.Repeat("a", maxBootstrapFileReadBytes-1) + "😀" + "tail"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := readBootstrapFile(path)
	if !ok {
		t.Fatal("expected the file to be readable")
	}
	if len(got) >= len(data) {
		t.Fatalf("read %d bytes, want fewer than the %d-byte file", len(got), len(data))
	}
	if len(got) > maxBootstrapFileReadBytes {
		t.Fatalf("read %d bytes, want at most %d", len(got), maxBootstrapFileReadBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatal("disk-truncated content is not valid UTF-8")
	}
}

func TestLoadAgentDefinitionCapsRetainedContent(t *testing.T) {
	large := strings.Repeat("z", 5000)
	ws := setupWorkspace(t, map[string]string{
		"AGENT.md": large,
		"SOUL.md":  large,
		"USER.md":  large,
	})
	defer cleanupWorkspace(t, ws)

	definition := NewContextBuilder(ws).
		WithBootstrapLimits(1000, 2400).
		LoadAgentDefinition()

	if definition.Agent == nil || definition.Soul == nil || definition.User == nil {
		t.Fatal("expected all bootstrap definitions to be loaded")
	}
	for name, content := range map[string]string{
		"agent body": definition.Agent.Body,
		"agent raw":  definition.Agent.Raw,
		"soul":       definition.Soul.Content,
		"user":       definition.User.Content,
	} {
		if n := utf8.RuneCountInString(content); n > 1000 {
			t.Errorf("%s is %d chars, want at most 1000", name, n)
		}
		if !strings.Contains(content, "bootstrap file truncated") {
			t.Errorf("%s is missing the truncation notice", name)
		}
	}
}

func TestLoadBootstrapFilesCapsLegacyIdentity(t *testing.T) {
	ws := setupWorkspace(t, map[string]string{
		"AGENTS.md":   "# Legacy agent",
		"IDENTITY.md": strings.Repeat("i", 5000),
	})
	defer cleanupWorkspace(t, ws)

	bootstrap := NewContextBuilder(ws).
		WithBootstrapLimits(1000, 2400).
		LoadBootstrapFiles()

	const prefix = "## IDENTITY.md\n\n"
	if !strings.Contains(bootstrap, prefix) {
		t.Fatalf("expected IDENTITY.md section: %q", bootstrap)
	}
	section := bootstrap[strings.Index(bootstrap, prefix)+len(prefix):]
	section = strings.TrimSuffix(section, "\n\n")
	if n := utf8.RuneCountInString(section); n > 1000 {
		t.Fatalf("identity section is %d chars, want at most 1000", n)
	}
	if !strings.Contains(section, "read_file on IDENTITY.md") {
		t.Fatalf("identity notice must point at IDENTITY.md: %q", section)
	}
}

func TestOversizedUserMarkdownStaysBounded(t *testing.T) {
	ws := setupWorkspace(t, map[string]string{
		"USER.md": strings.Repeat("u", 20000),
	})
	defer cleanupWorkspace(t, ws)

	system := NewContextBuilder(ws).
		BuildMessagesFromPrompt(PromptBuildRequest{SessionKey: "s", CurrentMessage: "hi"})[0].Content
	if !strings.Contains(system, "USER.md truncated") {
		t.Fatalf("prompt must report the bounded USER.md: %q", system)
	}
	if n := utf8.RuneCountInString(system); n > 12000 {
		t.Fatalf("system prompt is %d chars, USER.md was not bounded", n)
	}
}

func TestBootstrapLimitsRebuildCachedPrompt(t *testing.T) {
	ws := setupWorkspace(t, map[string]string{
		"AGENT.md": strings.Repeat("q", 2000),
	})
	defer cleanupWorkspace(t, ws)

	cb := NewContextBuilder(ws)
	before := cb.BuildSystemPromptWithCache()
	if strings.Contains(before, "bootstrap file truncated") {
		t.Fatal("default cap should not truncate a 2000-character file")
	}

	cb.WithBootstrapLimits(500, 2400)
	after := cb.BuildSystemPromptWithCache()
	if after == before {
		t.Fatal("changing the bootstrap limits must invalidate the cached prompt")
	}
	if !strings.Contains(after, "bootstrap file truncated") {
		t.Fatalf("new cap was not applied: %q", after)
	}
}
