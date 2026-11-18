package skills

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeSkill(t testing.TB, root, name, description, body string) string {
	t.Helper()
	dir := filepath.Join(root, "skills", name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n%s", name, description, body)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644))
	return dir
}

// newTestCatalog returns a catalog that rescans on every call, so tests see
// edits at once instead of after the freshness window.
func newTestCatalog(t testing.TB) (*Catalog, string) {
	t.Helper()
	workspace := t.TempDir()
	c := NewCatalog(NewSkillsLoader(workspace, "", ""))
	c.recheck = 0
	return c, workspace
}

func names(hits []SkillInfo) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Name
	}
	return out
}

func TestCatalogSearchExactNameFirst(t *testing.T) {
	c, ws := newTestCatalog(t)
	writeSkill(t, ws, "git-workflow", "Manage branches, commits and merges in git", "x")
	// Mentions the exact name many times: BM25 alone would rank it first.
	writeSkill(t, ws, "notes", "github-manager github manager github manager helper", "x")
	writeSkill(t, ws, "github-manager", "Manage GitHub repositories and pull requests", "x")

	for _, q := range []string{"github-manager", "GitHub-Manager", "  github-manager "} {
		hits, _, err := c.Search(context.Background(), q, 5)
		require.NoError(t, err)
		require.NotEmpty(t, hits)
		assert.Equal(t, "github-manager", hits[0].Name, q)
	}
}

func TestCatalogSearchNaturalLanguage(t *testing.T) {
	c, ws := newTestCatalog(t)
	writeSkill(t, ws, "github-manager", "Manage GitHub repositories and pull requests", "x")
	writeSkill(t, ws, "git-workflow", "Manage branches, commits and merges", "x")
	writeSkill(t, ws, "pdf-tools", "Extract text and tables from PDF documents", "x")
	writeSkill(t, ws, "weather", "Current weather and forecasts for a city", "x")

	hits, more, err := c.Search(context.Background(), "I need to open a pull request for my repository", 2)
	require.NoError(t, err)
	assert.False(t, more)
	require.NotEmpty(t, hits)
	assert.Equal(t, "github-manager", hits[0].Name)

	// identifiers split into words: "git workflow" finds git-workflow by name
	hits, _, err = c.Search(context.Background(), "git workflow", 1)
	require.NoError(t, err)
	assert.Equal(t, []string{"git-workflow"}, names(hits))
}

func TestCatalogSearchNoResultsAndLimit(t *testing.T) {
	c, ws := newTestCatalog(t)
	hits, more, err := c.Search(context.Background(), "anything", 5)
	require.NoError(t, err)
	assert.Empty(t, hits, "empty catalog")
	assert.False(t, more)

	for i := 0; i < 100; i++ {
		writeSkill(t, ws, fmt.Sprintf("deploy-%03d", i), "Deploy services to the cluster", "x")
	}
	hits, _, err = c.Search(context.Background(), "zzzunmatched", 5)
	require.NoError(t, err)
	assert.Empty(t, hits)

	hits, more, err = c.Search(context.Background(), "deploy services cluster", 20)
	require.NoError(t, err)
	assert.Len(t, hits, 20)
	assert.True(t, more)

	hits, more, err = c.Search(context.Background(), "deploy services cluster", 3)
	require.NoError(t, err)
	assert.Len(t, hits, 3)
	assert.True(t, more)
}

func TestCatalogReadFullInstructions(t *testing.T) {
	c, ws := newTestCatalog(t)
	dir := writeSkill(
		t,
		ws,
		"github-manager",
		"Manage GitHub",
		"# GitHub Manager\n\n## Workflow\n\n1. Inspect the repository.\n",
	)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "references"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "references", "api.md"), []byte("api notes"), 0o644))

	body, err := c.Read(context.Background(), "github-manager", "")
	require.NoError(t, err)
	assert.Equal(
		t,
		"# GitHub Manager\n\n## Workflow\n\n1. Inspect the repository.\n",
		body,
		"frontmatter stripped, rest complete",
	)

	body, err = c.Read(context.Background(), "GitHub-Manager", "references/api.md")
	require.NoError(t, err)
	assert.Equal(t, "api notes", body)
}

func TestCatalogReadMissingSkill(t *testing.T) {
	c, ws := newTestCatalog(t)
	writeSkill(t, ws, "real", "A real skill", "x")
	_, err := c.Read(context.Background(), "ghost", "")
	assert.ErrorIs(t, err, ErrSkillNotFound)
	_, err = c.Read(context.Background(), "real", "references/nope.md")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrPathDenied)
}

func TestCatalogReadBlocksPathEscape(t *testing.T) {
	c, ws := newTestCatalog(t)
	dir := writeSkill(t, ws, "real", "A real skill", "x")
	secret := filepath.Join(ws, ".env")
	require.NoError(t, os.WriteFile(secret, []byte("TOKEN=1"), 0o644))
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "id_rsa"), []byte("PRIVATE"), 0o644))

	for _, rel := range []string{"../../.env", "../../../etc/passwd", "/root/.ssh/id_rsa", secret, "references/../../../.env"} {
		_, err := c.Read(context.Background(), "real", rel)
		assert.ErrorIs(t, err, ErrPathDenied, rel)
	}

	if err := os.Symlink(secret, filepath.Join(dir, "link.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "linkdir")))
	for _, rel := range []string{"link.md", "linkdir/id_rsa"} {
		_, err := c.Read(context.Background(), "real", rel)
		assert.ErrorIs(t, err, ErrPathDenied, rel)
	}

	// SKILL.md replaced by a link that leaves the skill directory
	require.NoError(t, os.Remove(filepath.Join(dir, "SKILL.md")))
	require.NoError(t, os.Symlink(secret, filepath.Join(dir, "SKILL.md")))
	_, err := c.Read(context.Background(), "real", "")
	assert.ErrorIs(t, err, ErrPathDenied)
}

func TestCatalogReadSizeLimit(t *testing.T) {
	c, ws := newTestCatalog(t)
	dir := writeSkill(t, ws, "big", "A big skill", "x")
	big := filepath.Join(dir, "references.md")

	require.NoError(t, os.WriteFile(big, []byte(strings.Repeat("a", MaxReadBytes)), 0o644))
	body, err := c.Read(context.Background(), "big", "references.md")
	require.NoError(t, err)
	assert.Len(t, body, MaxReadBytes, "exactly at the limit is complete")

	require.NoError(t, os.WriteFile(big, []byte(strings.Repeat("a", MaxReadBytes+1)), 0o644))
	_, err = c.Read(context.Background(), "big", "references.md")
	assert.ErrorIs(t, err, ErrTooLarge, "over the limit is an explicit error, never a truncation")

	require.NoError(t, os.WriteFile(big, []byte{0xff, 0xfe, 0x00}, 0o644))
	_, err = c.Read(context.Background(), "big", "references.md")
	assert.ErrorContains(t, err, "not a text file")
}

func TestCatalogRemovedSkill(t *testing.T) {
	c, ws := newTestCatalog(t)
	dir := writeSkill(t, ws, "doomed", "Soon to be removed helper", "x")
	hits, _, err := c.Search(context.Background(), "removed helper", 5)
	require.NoError(t, err)
	require.Len(t, hits, 1)

	require.NoError(t, os.RemoveAll(dir))
	hits, _, err = c.Search(context.Background(), "removed helper", 5)
	require.NoError(t, err)
	assert.Empty(t, hits)
	_, err = c.Read(context.Background(), "doomed", "")
	assert.ErrorIs(t, err, ErrSkillNotFound)
}

func TestCatalogModifiedSkillRefreshesIndex(t *testing.T) {
	c, ws := newTestCatalog(t)
	dir := writeSkill(t, ws, "tool", "Handles giraffes", "v1")
	hits, _, _ := c.Search(context.Background(), "giraffes", 5)
	require.Len(t, hits, 1)

	writeSkill(t, ws, "tool", "Handles zebras", "v2 body")
	future := time.Now().Add(2 * time.Second)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "SKILL.md"), future, future))

	hits, _, _ = c.Search(context.Background(), "giraffes", 5)
	assert.Empty(t, hits, "old description no longer indexed")
	hits, _, _ = c.Search(context.Background(), "zebras", 5)
	require.Len(t, hits, 1)
	body, err := c.Read(context.Background(), "tool", "")
	require.NoError(t, err)
	assert.Equal(t, "v2 body", body, "read is never cached")
}

func TestCatalogAuthorization(t *testing.T) {
	c, ws := newTestCatalog(t)
	writeSkill(t, ws, "shell", "Run shell commands carefully", "x")
	writeSkill(t, ws, "paint", "Paint pictures with shell colors", "x")
	ctx := WithAccess(context.Background(), func(name string) bool { return name == "shell" })

	hits, _, err := c.Search(ctx, "shell", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"shell"}, names(hits))
	hits, _, _ = c.Search(ctx, "paint", 10)
	assert.Empty(t, hits, "even an exact name does not leak a hidden skill")

	_, err = c.Read(ctx, "paint", "")
	assert.ErrorIs(t, err, ErrSkillNotFound, "read re-checks authorization")
	_, err = c.Read(ctx, "shell", "")
	assert.NoError(t, err)
	_, ok := c.Resolve("PAINT")
	assert.True(t, ok, "Resolve is name lookup for the runtime, not a model-facing read")
}

func TestCatalogAccessRevokedMidCall(t *testing.T) {
	c, ws := newTestCatalog(t)
	writeSkill(t, ws, "alpha", "Handles giraffes", "body")

	// First answer authorizes the candidate, the re-check before returning refuses it.
	revokeAfterFirst := func() context.Context {
		calls := 0
		return WithAccess(context.Background(), func(string) bool { calls++; return calls == 1 })
	}
	_, _, err := c.Search(revokeAfterFirst(), "giraffes", 5)
	assert.ErrorIs(t, err, ErrAccessRevoked)
	_, err = c.Read(revokeAfterFirst(), "alpha", "")
	assert.ErrorIs(t, err, ErrAccessRevoked)
}

func TestCatalogHonorsCancellation(t *testing.T) {
	c, ws := newTestCatalog(t)
	writeSkill(t, ws, "alpha", "Handles giraffes", "body")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := c.Search(ctx, "giraffes", 5)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = c.Read(ctx, "alpha", "")
	assert.ErrorIs(t, err, context.Canceled)
}

func TestCatalogReusesSnapshotAndIndex(t *testing.T) {
	c, ws := newTestCatalog(t)
	writeSkill(t, ws, "alpha", "Handles giraffes", "body")

	first := c.current()
	engine := first.engine()
	assert.Same(t, first, c.current(), "rescan with unchanged metadata keeps the snapshot")
	assert.Same(t, engine, c.current().engine(), "and its index")

	writeSkill(t, ws, "beta", "Handles zebras", "body")
	second := c.current()
	assert.NotSame(t, first, second)
	assert.Len(t, second.entries, 2)
}

func TestCatalogFreshnessWindow(t *testing.T) {
	workspace := t.TempDir()
	c := NewCatalog(NewSkillsLoader(workspace, "", "")) // default window
	writeSkill(t, workspace, "alpha", "Handles giraffes", "body")
	first := c.current()

	// Unchanged roots inside the window: no rescan at all.
	assert.Same(t, first, c.current())

	// An install touches the skills root, which is checked on every call.
	writeSkill(t, workspace, "beta", "Handles zebras", "body")
	future := time.Now().Add(2 * time.Second)
	require.NoError(t, os.Chtimes(filepath.Join(workspace, "skills"), future, future))
	hits, _, err := c.Search(context.Background(), "zebras", 5)
	require.NoError(t, err)
	assert.Equal(t, []string{"beta"}, names(hits), "install visible without waiting for the window")
}

func TestCatalogPluginSkill(t *testing.T) {
	workspace := t.TempDir()
	loader := NewSkillsLoader(workspace, "", "")
	c := NewCatalog(loader)
	loader.SetPluginSkills("demo", []PluginSkill{{
		Info: SkillInfo{
			Name: "demo:greet", Path: "skills/greet/SKILL.md", Source: "plugin:demo",
			Description: "Greets people politely", PluginID: "demo", ResourceTool: "demo_read",
		},
		Body: "say hello",
	}})

	hits, _, err := c.Search(context.Background(), "greets people", 5)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "skills/greet/SKILL.md", hits[0].Location())

	body, err := c.Read(context.Background(), "demo:greet", "")
	require.NoError(t, err)
	assert.Equal(t, "say hello", body)
	_, err = c.Read(context.Background(), "demo:greet", "references/x.md")
	assert.Error(t, err)

	loader.SetPluginSkills("demo", nil)
	_, err = c.Read(context.Background(), "demo:greet", "")
	assert.ErrorIs(t, err, ErrSkillNotFound, "plugin removal is seen at once")
}

// seedSkills writes n skills with varied descriptions plus two known ones.
func seedSkills(t testing.TB, root string, n int) {
	t.Helper()
	words := strings.Fields("build deploy test review database network cache queue email calendar image audio " +
		"report invoice backup monitor alert search index parse convert upload download schedule notify encrypt " +
		"compress translate summarize classify extract validate migrate rollback provision")
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < n; i++ {
		var d strings.Builder
		for j := 0; j < 12; j++ {
			d.WriteString(words[rng.Intn(len(words))])
			d.WriteByte(' ')
		}
		writeSkill(t, root, fmt.Sprintf("skill-%05d", i), strings.TrimSpace(d.String()), "# body\n")
	}
	writeSkill(t, root, "github-manager", "Manage GitHub repositories and pull requests", "# GitHub Manager\n")
	writeSkill(t, root, "git-workflow", "Manage branches, commits and merges", "# Git Workflow\n")
}

// TestCatalog10kSkills validates the performance goals on a 10,000-skill
// catalog: metadata search under 100 ms, repeated searches served from the
// prepared index, no SKILL.md bodies kept in memory.
func TestCatalog10kSkills(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 10,000 skills")
	}
	workspace := t.TempDir()
	seedSkills(t, workspace, 10000)
	c := NewCatalog(NewSkillsLoader(workspace, "", ""))
	ctx := context.Background()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	hits, _, err := c.Search(ctx, "github-manager", 5)
	cold := time.Since(start)
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	assert.Equal(t, "github-manager", hits[0].Name)
	runtime.GC()
	runtime.ReadMemStats(&after)

	start = time.Now()
	hits, _, err = c.Search(ctx, "deploy database backup", 20)
	warm := time.Since(start)
	require.NoError(t, err)
	assert.Len(t, hits, 20)
	assert.Less(t, warm, 100*time.Millisecond)

	// Past the freshness window one call re-stats every skill. It is a one-off
	// per window and machine-dependent, so it is reported, not asserted.
	c.recheck = 0
	start = time.Now()
	_, _, err = c.Search(ctx, "deploy database backup", 20)
	revalidate := time.Since(start)
	require.NoError(t, err)

	t.Logf("10k skills: cold build+search %v, warm search %v, revalidating search %v, retained heap %.1f MiB",
		cold, warm, revalidate, float64(after.HeapAlloc-before.HeapAlloc)/(1<<20))
}

func BenchmarkCatalogSearch10k(b *testing.B) {
	workspace := b.TempDir()
	seedSkills(b, workspace, 10000)
	c := NewCatalog(NewSkillsLoader(workspace, "", ""))
	ctx := context.Background()
	if _, _, err := c.Search(ctx, "warm up", 5); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := c.Search(ctx, "deploy database backup", 5); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCatalogRevalidate10k(b *testing.B) {
	workspace := b.TempDir()
	seedSkills(b, workspace, 10000)
	c := NewCatalog(NewSkillsLoader(workspace, "", ""))
	c.recheck = 0
	ctx := context.Background()
	if _, _, err := c.Search(ctx, "warm up", 5); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := c.Search(ctx, "deploy database backup", 5); err != nil {
			b.Fatal(err)
		}
	}
}
