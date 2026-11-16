package evolution

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	envBuiltinSkills = "PICOCLAW_BUILTIN_SKILLS"
	envPicoclawHome  = "PICOCLAW_HOME"
)

// skillsDirs resolves the built-in and global skill directories without
// importing the core config package. A plugin process may run with a very
// small environment, so both helpers degrade to empty paths instead of
// failing.
func builtinSkillsDir() string {
	if dir := strings.TrimSpace(os.Getenv(envBuiltinSkills)); dir != "" {
		return dir
	}
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		return ""
	}
	return filepath.Join(wd, "skills")
}

func globalSkillsDir(workspace string) string {
	home := strings.TrimSpace(os.Getenv(envPicoclawHome))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err == nil && userHome != "" {
			home = filepath.Join(userHome, ".picoclaw")
		}
	}
	if home == "" {
		// The default workspace is <home>/workspace, so the sibling skills
		// directory still works when the host intentionally hides HOME from
		// plugin processes.
		cleaned := filepath.Clean(strings.TrimSpace(workspace))
		if filepath.Base(cleaned) == "workspace" {
			home = filepath.Dir(cleaned)
		}
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, "skills")
}
