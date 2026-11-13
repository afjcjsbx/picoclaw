// Package skills is the small skill-file reader used by the evolution plugin.
// It intentionally avoids the core skills package (and its registry, installer,
// and provider dependencies) so the opt-in plugin binary stays small.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const MaxNameLength = 64

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`)

type SkillInfo struct {
	Name        string
	Path        string
	Source      string
	Description string
}

type SkillsLoader struct {
	workspaceSkills string
	globalSkills    string
	builtinSkills   string
}

func NewSkillsLoader(workspace, globalSkills, builtinSkills string) *SkillsLoader {
	return &SkillsLoader{
		workspaceSkills: filepath.Join(workspace, "skills"),
		globalSkills:    globalSkills,
		builtinSkills:   builtinSkills,
	}
}

func ValidateSkillName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("skill name is required")
	}
	if filepath.IsAbs(trimmed) {
		return fmt.Errorf("skill name must not be an absolute path")
	}
	if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") || strings.Contains(trimmed, "..") {
		return fmt.Errorf("skill name must not contain path separators")
	}
	if len(trimmed) > MaxNameLength {
		return fmt.Errorf("skill name exceeds %d characters", MaxNameLength)
	}
	if !namePattern.MatchString(trimmed) {
		return fmt.Errorf("skill name must be alphanumeric with hyphens")
	}
	return nil
}

func (s *SkillsLoader) ListSkills() []SkillInfo {
	out := make([]SkillInfo, 0)
	seen := make(map[string]struct{})
	for _, root := range []struct {
		dir    string
		source string
	}{
		{s.workspaceSkills, "workspace"},
		{s.globalSkills, "global"},
		{s.builtinSkills, "builtin"},
	} {
		if strings.TrimSpace(root.dir) == "" {
			continue
		}
		entries, err := os.ReadDir(root.dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillFile := filepath.Join(root.dir, entry.Name(), "SKILL.md")
			if _, err := os.Stat(skillFile); err != nil {
				continue
			}
			info := SkillInfo{
				Name:   entry.Name(),
				Path:   skillFile,
				Source: root.source,
			}
			if name, description := readMetadata(skillFile); name != "" || description != "" {
				if name != "" {
					info.Name = name
				}
				info.Description = description
			}
			if err := ValidateSkillName(info.Name); err != nil {
				continue
			}
			if _, ok := seen[info.Name]; ok {
				continue
			}
			seen[info.Name] = struct{}{}
			out = append(out, info)
		}
	}
	return out
}

func (s *SkillsLoader) LoadSkill(name string) (string, bool) {
	if err := ValidateSkillName(name); err != nil {
		return "", false
	}
	for _, dir := range []string{s.workspaceSkills, s.globalSkills, s.builtinSkills} {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name, "SKILL.md"))
		if err == nil {
			return stripFrontmatter(string(data)), true
		}
	}
	return "", false
}

func readMetadata(path string) (name, description string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	frontmatter, _ := splitFrontmatter(string(data))
	for _, line := range strings.Split(frontmatter, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			name = value
		case "description":
			description = value
		}
	}
	return name, description
}

func splitFrontmatter(content string) (frontmatter, body string) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", content
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n")
		}
	}
	return "", content
}

func stripFrontmatter(content string) string {
	_, body := splitFrontmatter(content)
	return strings.TrimLeft(body, "\n")
}
