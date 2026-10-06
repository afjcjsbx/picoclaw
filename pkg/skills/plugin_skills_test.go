package skills

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginSkillValidationAndResolution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "greet", "SKILL.md")
	valid := "---\nname: greet\ndescription: Say hello\nmetadata:\n  author: example\n---\nHello."
	skill, err := ParsePluginSkill("demo.plugin", path, []byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	loader := NewSkillsLoader(t.TempDir(), "", "")
	loader.SetPluginSkills("demo.plugin", []PluginSkill{skill})
	if body, ok := loader.LoadSkill("demo.plugin:greet"); !ok || !strings.Contains(body, "Hello.") {
		t.Fatalf("%q %v", body, ok)
	}
	if !strings.Contains(loader.BuildSkillsSummary(), "demo.plugin:greet") {
		t.Fatal("missing summary")
	}
	loader.SetPluginSkills("demo.plugin", nil)
	if _, ok := loader.LoadSkill("demo.plugin:greet"); ok {
		t.Fatal("removed skill is still visible")
	}
	for _, invalid := range []string{
		"No frontmatter", strings.Replace(valid, "name: greet", "name: Greet", 1), strings.Replace(valid, "name: greet", "name: other", 1), strings.Replace(valid, "description: Say hello", "description: 123", 1), strings.Replace(valid, "author: example", "author: 123", 1),
	} {
		if _, err := ParsePluginSkill("demo", path, []byte(invalid)); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
}
