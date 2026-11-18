package integrationtools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/skills"
)

func skillTools(t *testing.T, skillsByName map[string]string) (*SkillsSearchTool, *SkillsReadTool, string) {
	t.Helper()
	workspace := t.TempDir()
	for name, description := range skillsByName {
		dir := filepath.Join(workspace, "skills", name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n# %s\n\nInstructions.", name, description, name)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644))
	}
	catalog := skills.NewCatalog(skills.NewSkillsLoader(workspace, "", ""))
	return NewSkillsSearchTool(catalog), NewSkillsReadTool(catalog), workspace
}

func decodeSearch(t *testing.T, res *ToolResult) skillsSearchOutput {
	t.Helper()
	require.False(t, res.IsError, res.ForLLM)
	var out skillsSearchOutput
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &out), res.ForLLM)
	return out
}

func TestSkillsSearchOutputShape(t *testing.T) {
	search, _, ws := skillTools(t, map[string]string{
		"github-manager": "Manage GitHub repositories & pull requests",
		"git-workflow":   "Manage branches, commits and merges",
	})

	res := search.Execute(context.Background(), map[string]any{"query": "github-manager"})
	out := decodeSearch(t, res)
	require.NotEmpty(t, out.Skills)
	assert.Equal(t, "github-manager", out.Skills[0].Name)
	assert.Equal(t, filepath.Join(ws, "skills", "github-manager", "SKILL.md"), out.Skills[0].Location)
	assert.Contains(t, res.ForLLM, "repositories & pull", "no HTML escaping inflating the payload")
	assert.True(t, res.Silent)

	res = search.Execute(context.Background(), map[string]any{"query": "no such capability xyzzy"})
	assert.Equal(t, `{"skills":[],"hasMore":false}`, res.ForLLM, "empty array, never null")
}

func TestSkillsSearchLimits(t *testing.T) {
	all := map[string]string{}
	for i := 0; i < 100; i++ {
		all[fmt.Sprintf("deploy-%03d", i)] = "Deploy services to the cluster"
	}
	search, _, _ := skillTools(t, all)
	ctx := context.Background()

	out := decodeSearch(t, search.Execute(ctx, map[string]any{"query": "deploy services", "limit": float64(500)}))
	assert.Len(t, out.Skills, 20, "100 relevant skills: at most 20 returned")
	assert.True(t, out.HasMore)

	out = decodeSearch(t, search.Execute(ctx, map[string]any{"query": "deploy services"}))
	assert.Len(t, out.Skills, 5, "default limit")

	out = decodeSearch(t, search.Execute(ctx, map[string]any{"query": "deploy services", "limit": float64(0)}))
	assert.Len(t, out.Skills, 1, "limit is clamped to at least 1")

	for _, args := range []map[string]any{
		{},
		{"query": "   "},
		{"query": strings.Repeat("x", 1001)},
		{"query": "ok", "limit": "many"},
	} {
		assert.True(t, search.Execute(ctx, args).IsError, "%v", args)
	}
	assert.False(t, search.Execute(ctx, map[string]any{"query": strings.Repeat("x", 1000)}).IsError)
}

func TestSkillsSearchBoundsDescriptionsAndOutput(t *testing.T) {
	long := strings.Repeat("deploy ", 140) // ~980 chars, over the 512 cap
	all := map[string]string{}
	for i := 0; i < 40; i++ {
		all[fmt.Sprintf("long-%02d-%s", i, strings.Repeat("n", 40))] = long
	}
	search, _, _ := skillTools(t, all)

	res := search.Execute(context.Background(), map[string]any{"query": "deploy", "limit": float64(20)})
	out := decodeSearch(t, res)
	for _, s := range out.Skills {
		assert.LessOrEqual(t, utf8.RuneCountInString(s.Description), 512)
	}
	assert.LessOrEqual(t, utf8.RuneCountInString(res.ForLLM), 16000)
	assert.NotEmpty(t, out.Skills)
	assert.True(t, out.HasMore)
}

func TestSkillsReadTool(t *testing.T) {
	_, read, ws := skillTools(t, map[string]string{"github-manager": "Manage GitHub"})
	ctx := context.Background()

	res := read.Execute(ctx, map[string]any{"name": "github-manager"})
	require.False(t, res.IsError, res.ForLLM)
	assert.Equal(t, "# github-manager\n\nInstructions.", res.ForLLM)
	assert.True(t, res.LargeResultSpilled, "a complete skill must not be reduced to a spill preview")

	for _, args := range []map[string]any{
		{},
		{"name": "ghost"},
		{"name": "github-manager", "relativePath": "../../.env"},
		{"name": "github-manager", "relativePath": "/etc/passwd"},
	} {
		assert.True(t, read.Execute(ctx, args).IsError, "%v", args)
	}

	big := filepath.Join(ws, "skills", "github-manager", "big.md")
	require.NoError(t, os.WriteFile(big, []byte(strings.Repeat("x", skills.MaxReadBytes+1)), 0o644))
	res = read.Execute(ctx, map[string]any{"name": "github-manager", "relativePath": "big.md"})
	assert.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "too large")

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	res = read.Execute(canceled, map[string]any{"name": "github-manager"})
	assert.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "aborted")
}
