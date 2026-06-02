package integrationtools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/skills"
)

const maxSkillSupportFiles = 50

type SkillViewTool struct {
	loader *skills.SkillsLoader
}

func NewSkillViewTool(workspace string) *SkillViewTool {
	return &SkillViewTool{loader: newSkillsLoaderForWorkspace(workspace)}
}

func (t *SkillViewTool) Name() string {
	return "skill_view"
}

func (t *SkillViewTool) Description() string {
	return "Load the full contents of an installed skill or one supporting file from that skill. Use /skills or /<skill-name> to discover skills, then use this tool when you need exact skill instructions."
}

func (t *SkillViewTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Installed skill name to inspect.",
			},
			"file_path": map[string]any{
				"type":        "string",
				"description": "Optional relative path inside the skill directory, for example references/checklist.md or scripts/run.sh.",
			},
		},
		"required": []string{"name"},
	}
}

func (t *SkillViewTool) Execute(_ context.Context, args map[string]any) *ToolResult {
	if t == nil || t.loader == nil {
		return ErrorResult("skill view is unavailable")
	}

	name, _ := args["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrorResult("name is required and must be a non-empty string")
	}

	skill, ok := t.loader.FindSkill(name)
	if !ok {
		return ErrorResult(fmt.Sprintf("skill %q is not installed", name))
	}

	filePath, _ := args["file_path"].(string)
	filePath = strings.TrimSpace(filePath)

	content, _, err := t.loader.LoadSkillFile(skill.Name, filePath)
	if err != nil {
		return ErrorResult(formatSkillViewError(skill.Name, filePath, err))
	}

	var sb strings.Builder
	if filePath == "" {
		sb.WriteString(fmt.Sprintf("Skill: %s\n", skill.Name))
		if skill.Description != "" {
			sb.WriteString(fmt.Sprintf("Description: %s\n", skill.Description))
		}
		sb.WriteString(fmt.Sprintf("Source: %s\n", skill.Source))
		sb.WriteString(fmt.Sprintf("Path: %s\n\n", skill.Path))
		sb.WriteString(content)

		files, _, filesErr := t.loader.ListSkillFiles(skill.Name)
		if filesErr == nil && len(files) > 0 {
			sb.WriteString("\n\nSupporting files:\n")
			limit := minInt(len(files), maxSkillSupportFiles)
			for _, rel := range files[:limit] {
				sb.WriteString("- ")
				sb.WriteString(rel)
				sb.WriteByte('\n')
			}
			if len(files) > limit {
				sb.WriteString(fmt.Sprintf("- ... %d more\n", len(files)-limit))
			}
			sb.WriteString("\nUse skill_view with file_path to load one supporting file.")
		}
		return SilentResult(sb.String())
	}

	sb.WriteString(fmt.Sprintf("Skill: %s\n", skill.Name))
	sb.WriteString(fmt.Sprintf("File: %s\n\n", filePath))
	sb.WriteString(content)
	return SilentResult(sb.String())
}

func newSkillsLoaderForWorkspace(workspace string) *skills.SkillsLoader {
	builtinSkillsDir := strings.TrimSpace(os.Getenv(config.EnvBuiltinSkills))
	if builtinSkillsDir == "" {
		wd, _ := os.Getwd()
		builtinSkillsDir = filepath.Join(wd, "skills")
	}
	globalSkillsDir := filepath.Join(config.GetHome(), "skills")
	return skills.NewSkillsLoader(workspace, globalSkillsDir, builtinSkillsDir)
}

func formatSkillViewError(skillName, filePath string, err error) string {
	if os.IsNotExist(err) {
		if strings.TrimSpace(filePath) == "" {
			return fmt.Sprintf("skill %q could not be read: %v", skillName, err)
		}
		return fmt.Sprintf("file %q was not found in skill %q", filePath, skillName)
	}
	if strings.TrimSpace(filePath) == "" {
		return fmt.Sprintf("failed to load skill %q: %v", skillName, err)
	}
	return fmt.Sprintf("failed to load %q from skill %q: %v", filePath, skillName, err)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
