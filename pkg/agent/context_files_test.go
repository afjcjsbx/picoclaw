package agent

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestResolveContextFileMaxChars(t *testing.T) {
	tests := []struct {
		name       string
		configured int
		window     int
		want       int
	}{
		{name: "configured wins", configured: 4096, window: 131072, want: 4096},
		{name: "unknown window uses floor", configured: 0, window: 0, want: defaultContextFileMaxChars},
		{name: "small window keeps floor", configured: 0, window: 8192, want: defaultContextFileMaxChars},
		{name: "window scales cap", configured: 0, window: 131072, want: 31457},
		{name: "huge window capped by ceiling", configured: 0, window: 10_000_000, want: contextFileMaxCharsCeiling},
		{name: "non-positive configured falls back to window", configured: -1, window: 131072, want: 31457},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveContextFileMaxChars(tt.configured, tt.window); got != tt.want {
				t.Fatalf("resolveContextFileMaxChars(%d, %d) = %d, want %d",
					tt.configured, tt.window, got, tt.want)
			}
		})
	}
}

func TestTruncateContextFileSectionKeepsHeadTailAndMarker(t *testing.T) {
	section := "A" + strings.Repeat("b", 5000) + "Z"
	out := truncateContextFileSection(section, "AGENT.md", "AGENT.md", 1000)

	runes := []rune(section)
	if !strings.HasPrefix(out, string(runes[:700])) {
		t.Fatal("truncated section must keep the head of the file")
	}
	if !strings.HasSuffix(out, string(runes[len(runes)-200:])) {
		t.Fatal("truncated section must keep the tail of the file")
	}
	want := "\n\n[...truncated AGENT.md: kept 700+200 of 5002 chars. The middle is omitted. " +
		"If you need the full instructions, read the complete file with the read_file tool: AGENT.md]\n\n"
	if !strings.Contains(out, want) {
		t.Fatalf("marker missing from truncated section, got %q", out)
	}
}

func TestTruncateContextFileSectionLeavesContentAtCapUntouched(t *testing.T) {
	// The rendered section ("## AGENT.md\n\n" + body + "\n\n") is exactly 1000
	// runes, so the cap must not fire.
	body := strings.Repeat("x", 1000-len("## AGENT.md\n\n")-len("\n\n"))
	section := fmt.Sprintf("## AGENT.md\n\n%s\n\n", body)

	if out := truncateContextFileSection(section, "AGENT.md", "AGENT.md", 1000); out != section {
		t.Fatal("content at the cap must be injected verbatim")
	}
}

func TestTruncateContextFileSectionCountsRunesNotBytes(t *testing.T) {
	section := "頭" + strings.Repeat("中", 2000) + "尾"
	out := truncateContextFileSection(section, "SOUL.md", "SOUL.md", 512)

	if !utf8.ValidString(out) {
		t.Fatal("truncated section must stay valid UTF-8")
	}
	if !strings.HasPrefix(out, "頭") {
		t.Fatal("truncated section must keep the first rune")
	}
	if !strings.HasSuffix(out, "尾") {
		t.Fatal("truncated section must keep the last rune")
	}
	if !strings.Contains(out, "kept 358+102 of 2002 chars") {
		t.Fatalf("marker must count runes, got %q", out)
	}
}

func TestTruncateContextFileSectionListsOmittedHeadings(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("## AGENT.md\n\n")
	sb.WriteString("# Kept Head\n")
	for utf8.RuneCountInString(sb.String()) < 1200 {
		sb.WriteString("filler\n")
	}
	sb.WriteString("# Omitted One\n")
	sb.WriteString("```\n# fenced comment\n```\n")
	sb.WriteString("# Omitted Two\n")
	sb.WriteString(strings.Repeat("tail filler\n", 100))

	out := truncateContextFileSection(sb.String(), "AGENT.md", "AGENT.md", 1000)

	if !strings.Contains(out, "The middle is omitted. Omitted sections: Omitted One; Omitted Two.") {
		t.Fatalf("marker must name the omitted headings, got %q", out)
	}
	if strings.Contains(out, "Kept Head;") || strings.Contains(out, "fenced comment") {
		t.Fatalf("marker must not list headings kept in the excerpt or fenced code, got %q", out)
	}
}

func TestOmittedContextFileHeadingsLimitsList(t *testing.T) {
	var sb strings.Builder
	for i := range 20 {
		fmt.Fprintf(&sb, "# Heading %d\n", i)
	}

	got := omittedContextFileHeadings(sb.String(), 0, utf8.RuneCountInString(sb.String()))
	if len(got) != contextFileOmittedHeadingsLimit+1 {
		t.Fatalf("got %d headings, want %d plus ellipsis", len(got), contextFileOmittedHeadingsLimit)
	}
	if got[0] != "Heading 0" || got[contextFileOmittedHeadingsLimit-1] != "Heading 14" || got[len(got)-1] != "..." {
		t.Fatalf("unexpected heading list %v", got)
	}
}

func TestLoadBootstrapFilesTruncatesOversizedContextFile(t *testing.T) {
	big := "# Big\n" + strings.Repeat("context line\n", 3000)
	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md": big,
		"SOUL.md":  "# Soul\nShort soul.",
	})
	defer cleanupWorkspace(t, tmpDir)

	bootstrap := NewContextBuilder(tmpDir).LoadBootstrapFiles()

	if !strings.Contains(bootstrap, "[...truncated AGENT.md: kept 14000+4000 of ") {
		t.Fatalf("expected default cap to truncate AGENT.md, got %q", bootstrap)
	}
	if !strings.Contains(bootstrap, "read_file tool: AGENT.md]") {
		t.Fatalf("marker must point at the full file, got %q", bootstrap)
	}
	if !strings.Contains(bootstrap, "Short soul.") {
		t.Fatal("small context files must stay intact")
	}
	if !strings.Contains(bootstrap, "# Big") {
		t.Fatal("head of the oversized file must stay in the prompt")
	}
}

func TestLoadBootstrapFilesHonorsConfiguredContextFileLimit(t *testing.T) {
	big := strings.Repeat("line\n", 1000)
	tmpDir := setupWorkspace(t, map[string]string{"AGENT.md": big})
	defer cleanupWorkspace(t, tmpDir)

	cb := NewContextBuilder(tmpDir).WithContextFileLimits(1000, 131072)
	bootstrap := cb.LoadBootstrapFiles()

	if !strings.Contains(bootstrap, "kept 700+200 of ") {
		t.Fatalf("configured cap must drive the excerpt sizes, got %q", bootstrap)
	}
}

func TestWithContextFileLimitsScalesWithContextWindow(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{"AGENT.md": "# Agent\nSmall."})
	defer cleanupWorkspace(t, tmpDir)

	cb := NewContextBuilder(tmpDir).WithContextFileLimits(0, 131072)
	if got := cb.contextFileLimit(); got != 31457 {
		t.Fatalf("contextFileLimit() = %d, want 31457", got)
	}

	cb = NewContextBuilder(tmpDir).WithContextFileLimits(0, 0)
	if got := cb.contextFileLimit(); got != defaultContextFileMaxChars {
		t.Fatalf("contextFileLimit() = %d, want %d", got, defaultContextFileMaxChars)
	}
}
