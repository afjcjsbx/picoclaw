package skills

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// PluginSkill contains a validated, immutable skill body. It avoids rereading a
// package path after publication (including paths replaced by symlinks).
type PluginSkill struct {
	Info SkillInfo
	Body string
}

// ParsePluginSkill implements Agent Skills validation without changing the
// permissive legacy workspace skill loader.
func ParsePluginSkill(pluginID, path string, content []byte) (PluginSkill, error) {
	var result PluginSkill
	front, body := splitFrontmatter(string(content))
	if front == "" {
		return result, fmt.Errorf("YAML frontmatter is required")
	}
	var fields map[string]any
	if err := yaml.Unmarshal([]byte(front), &fields); err != nil {
		return result, err
	}
	name, ok := fields["name"].(string)
	if !ok || name == "" || utf8.RuneCountInString(name) > 64 || name != filepath.Base(filepath.Dir(path)) {
		return result, fmt.Errorf("skill name must match directory and contain 1-64 characters")
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") || strings.Contains(name, "--") {
		return result, fmt.Errorf("invalid skill name")
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return result, fmt.Errorf("invalid skill name")
		}
	}
	description, ok := fields["description"].(string)
	if !ok || strings.TrimSpace(description) == "" || utf8.RuneCountInString(description) > 1024 {
		return result, fmt.Errorf("invalid skill description")
	}
	for _, key := range []string{"license", "compatibility", "allowed-tools"} {
		if value, exists := fields[key]; exists {
			s, ok := value.(string)
			if !ok {
				return result, fmt.Errorf("%s must be a string", key)
			}
			if key == "compatibility" && (s == "" || utf8.RuneCountInString(s) > 500) {
				return result, fmt.Errorf("invalid compatibility")
			}
		}
	}
	if raw, exists := fields["metadata"]; exists {
		metadata, ok := raw.(map[string]any)
		if !ok {
			return result, fmt.Errorf("metadata must be a string map")
		}
		for _, value := range metadata {
			if _, ok := value.(string); !ok {
				return result, fmt.Errorf("metadata must be a string map")
			}
		}
	}
	result.Info = SkillInfo{
		Name:        pluginID + ":" + name,
		Path:        path,
		Source:      "plugin:" + pluginID,
		Description: description,
		PluginID:    pluginID,
	}
	result.Body = body
	return result, nil
}

func (sl *SkillsLoader) SetPluginSkills(pluginID string, entries []PluginSkill) {
	sl.pluginMu.Lock()
	defer sl.pluginMu.Unlock()
	if sl.pluginSkills == nil {
		sl.pluginSkills = make(map[string][]PluginSkill)
	}
	if len(entries) == 0 {
		delete(sl.pluginSkills, pluginID)
		return
	}
	sl.pluginSkills[pluginID] = append([]PluginSkill(nil), entries...)
}

func (sl *SkillsLoader) listPluginSkills() []PluginSkill {
	sl.pluginMu.RLock()
	defer sl.pluginMu.RUnlock()
	var result []PluginSkill
	for _, entries := range sl.pluginSkills {
		result = append(result, entries...)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Info.Name < result[j].Info.Name })
	return result
}
