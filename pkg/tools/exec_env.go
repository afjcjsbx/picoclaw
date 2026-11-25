package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/isolation"
)

// defaultExecEnvAllowlist is the set of parent environment variables inherited
// by exec child processes. Everything else is stripped, so credentials such as
// API keys and tokens do not reach LLM-invoked commands by accident.
//
// Add explicit names only: wildcard patterns could leak secrets.
var defaultExecEnvAllowlist = map[string]bool{
	"PATH":        true,
	"HOME":        true,
	"USER":        true,
	"LANG":        true,
	"SHELL":       true,
	"TERM":        true,
	"PWD":         true,
	"OLDPWD":      true,
	"HOSTNAME":    true,
	"LOGNAME":     true,
	"TZ":          true,
	"DISPLAY":     true,
	"TMPDIR":      true,
	"EDITOR":      true,
	"PAGER":       true,
	"HTTP_PROXY":  true,
	"http_proxy":  true,
	"HTTPS_PROXY": true,
	"https_proxy": true,
	"NO_PROXY":    true,
	"no_proxy":    true,

	// Locale
	"LC_ALL":            true,
	"LC_CTYPE":          true,
	"LC_MESSAGES":       true,
	"LC_MONETARY":       true,
	"LC_NUMERIC":        true,
	"LC_TIME":           true,
	"LC_PAPER":          true,
	"LC_NAME":           true,
	"LC_ADDRESS":        true,
	"LC_TELEPHONE":      true,
	"LC_MEASUREMENT":    true,
	"LC_IDENTIFICATION": true,
	"LC_COLLATE":        true,
}

// newExecEnv builds the cached, sanitized base environment for exec child
// processes. Config env_set values are trusted and take precedence over
// inherited variables; the remaining parent values are inherited only when
// their names are allowlisted.
func newExecEnv(workingDir string, envSet map[string]string, extraAllowlist []string) map[string]string {
	env := make(map[string]string, len(envSet)+len(defaultExecEnvAllowlist)+len(platformEnvAllowlist())+8)
	for key, value := range envSet {
		env[execEnvKey(key)] = value
	}

	setPicoclawEnv(env, workingDir)

	inherit := func(name string) {
		key := execEnvKey(name)
		if _, exists := env[key]; exists {
			return
		}
		if value, ok := os.LookupEnv(name); ok {
			env[key] = value
		}
	}

	for name := range defaultExecEnvAllowlist {
		inherit(name)
	}
	for name := range platformEnvAllowlist() {
		inherit(name)
	}
	for _, name := range extraAllowlist {
		if name = strings.TrimSpace(name); name != "" {
			inherit(name)
		}
	}

	return env
}

// setPicoclawEnv exposes PicoClaw's own paths to child processes. The values
// are derived from the running process, not from LLM arguments, and are always
// set after config env_set so the agent keeps control of these names.
func setPicoclawEnv(env map[string]string, workspace string) {
	home := os.Getenv(config.EnvHome)
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil && userHome != "" {
			home = filepath.Join(userHome, pkg.DefaultPicoClawHome)
		}
	}
	if home != "" {
		env[config.EnvHome] = home
	}
	// On Linux with isolation enabled the config file is not mounted inside the
	// sandbox (it contains API keys). Keep PICOCLAW_HOME for workspace and tool
	// discovery but omit PICOCLAW_CONFIG so the child cannot reach host secrets.
	// ponytail: runtime check couples exec env to isolation state; remove once
	// a mount-manifest API lets the sandbox declare what it exposes.
	linuxIsolated := runtime.GOOS == "linux" && isolation.CurrentConfig().Enabled
	if !linuxIsolated {
		if configPath := os.Getenv(config.EnvConfig); configPath != "" {
			env[config.EnvConfig] = configPath
		} else if home != "" {
			env[config.EnvConfig] = filepath.Join(home, "config.json")
		}
	}

	if workspace != "" {
		env["PICOCLAW_AGENT_WORKSPACE"] = workspace
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		env["PICOCLAW_EXE"] = exe
	}
	serviceName := os.Getenv(config.EnvServiceName)
	if serviceName == "" {
		serviceName = "picoclaw"
	}
	env[config.EnvServiceName] = serviceName
}

// buildEnvList renders an environment map as sorted KEY=value entries once, so
// runEnv copies a slice per command instead of rebuilding and sorting the map.
func buildEnvList(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	list := make([]string, 0, len(keys))
	for _, key := range keys {
		list = append(list, key+"="+env[key])
	}
	return list
}

// runEnv returns the cached child environment plus the trusted PICOCLAW_EXEC_*
// values. timeout is the effective foreground timeout; background sessions pass
// 0 because they are not time-limited. The model cannot add variables here:
// per-call env was removed because it could bypass the command and workspace
// guards.
func (t *ExecTool) runEnv(timeout time.Duration) []string {
	base := t.baseEnvList
	if base == nil {
		// Tests may build an ExecTool literal without the precomputed list.
		base = buildEnvList(t.baseEnv)
	}
	result := make([]string, 0, len(base)+2)
	result = append(result, base...)
	result = append(result,
		"PICOCLAW_EXEC_TIME="+time.Now().Format(time.RFC3339),
		"PICOCLAW_EXEC_TIMEOUT="+timeout.String(),
	)
	return result
}
