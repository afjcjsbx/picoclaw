package integrationtools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
)

func createLocalSkillFixture(t *testing.T, workspace string) {
	t.Helper()
	skillDir := filepath.Join(workspace, "skills", "shell")
	require.NoError(t, os.MkdirAll(filepath.Join(skillDir, "references"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: shell\ndescription: Shell helper\n---\n\n# shell\n\nPrefer concise shell commands."),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(skillDir, "references", "cheatsheet.md"),
		[]byte("ls -la\npwd"),
		0o644,
	))
}

func isolateSkillToolEnv(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv(config.EnvHome, filepath.Join(tmp, "home"))
	t.Setenv(config.EnvBuiltinSkills, filepath.Join(tmp, "builtin-skills"))
}

func TestSkillViewToolLoadsSkillAndSupportingFiles(t *testing.T) {
	isolateSkillToolEnv(t)
	workspace := t.TempDir()
	createLocalSkillFixture(t, workspace)

	tool := NewSkillViewTool(workspace)
	result := tool.Execute(context.Background(), map[string]any{
		"name": "shell",
	})

	require.False(t, result.IsError)
	assert.Contains(t, result.ForLLM, "Skill: shell")
	assert.Contains(t, result.ForLLM, "Prefer concise shell commands.")
	assert.Contains(t, result.ForLLM, "references/cheatsheet.md")
}

func TestSkillViewToolLoadsSpecificFile(t *testing.T) {
	isolateSkillToolEnv(t)
	workspace := t.TempDir()
	createLocalSkillFixture(t, workspace)

	tool := NewSkillViewTool(workspace)
	result := tool.Execute(context.Background(), map[string]any{
		"name":      "shell",
		"file_path": "references/cheatsheet.md",
	})

	require.False(t, result.IsError)
	assert.Contains(t, result.ForLLM, "File: references/cheatsheet.md")
	assert.Contains(t, result.ForLLM, "ls -la")
}

func TestSkillViewToolRejectsTraversal(t *testing.T) {
	isolateSkillToolEnv(t)
	workspace := t.TempDir()
	createLocalSkillFixture(t, workspace)

	tool := NewSkillViewTool(workspace)
	result := tool.Execute(context.Background(), map[string]any{
		"name":      "shell",
		"file_path": "../secret.txt",
	})

	require.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "invalid")
}
