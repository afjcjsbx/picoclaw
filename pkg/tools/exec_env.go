package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
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

// windowsExecEnvAllowlist contains additional variables needed to run commands
// on Windows, where the default Unix set is not enough.
var windowsExecEnvAllowlist = map[string]bool{
	"PATHEXT":      true,
	"SYSTEMROOT":   true,
	"SYSTEMDRIVE":  true,
	"WINDIR":       true,
	"COMSPEC":      true,
	"APPDATA":      true,
	"LOCALAPPDATA": true,
	"PROGRAMDATA":  true,
	"USERPROFILE":  true,
	"HOMEDRIVE":    true,
	"HOMEPATH":     true,
	"TEMP":         true,
	"TMP":          true,
}

// execEnvBlocklist lists variables the LLM cannot set, even through the env
// argument. They control process behavior (command resolution, user identity,
// dynamic loader behavior) or belong to PicoClaw itself.
var execEnvBlocklist = map[string]bool{
	"PATH":            true,
	"HOME":            true,
	"USER":            true,
	"LOGNAME":         true,
	"SHELL":           true,
	"LD_PRELOAD":      true,
	"LD_LIBRARY_PATH": true,
	"LD_AUDIT":        true,
	"LD_DEBUG":        true,

	// PICOCLAW_* vars are controlled by the agent, not the LLM.
	"PICOCLAW_HOME":            true,
	"PICOCLAW_CONFIG":          true,
	"PICOCLAW_AGENT_WORKSPACE": true,
	"PICOCLAW_EXE":             true,
	"PICOCLAW_SERVICE_NAME":    true,
	"PICOCLAW_EXEC_TIME":       true,
	"PICOCLAW_EXEC_TIMEOUT":    true,
}

// execEnvBlocklistPrefixes are prefixes the LLM cannot set.
var execEnvBlocklistPrefixes = []string{
	"PICOCLAW_",
}

// newExecEnv builds the cached, sanitized base environment for exec child
// processes. Config env_set values are trusted and take precedence over
// inherited variables; the remaining parent values are inherited only when
// their names are allowlisted.
func newExecEnv(workingDir string, envSet map[string]string, extraAllowlist []string) map[string]string {
	env := make(map[string]string, len(envSet)+len(defaultExecEnvAllowlist)+len(windowsExecEnvAllowlist)+8)
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
	if runtime.GOOS == "windows" {
		for name := range windowsExecEnvAllowlist {
			inherit(name)
		}
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
	if configPath := os.Getenv(config.EnvConfig); configPath != "" {
		env[config.EnvConfig] = configPath
	} else if home != "" {
		env[config.EnvConfig] = filepath.Join(home, "config.json")
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

// execEnvKey normalizes a variable name. Windows environment names are
// case-insensitive, so keys are uppercased there to avoid duplicate entries
// such as Path and PATH.
func execEnvKey(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}

// isBlockedExecEnv reports whether the LLM may set the given variable.
func isBlockedExecEnv(key string) bool {
	key = execEnvKey(key)
	if execEnvBlocklist[key] {
		return true
	}
	for _, prefix := range execEnvBlocklistPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// execEnvFromArgs parses the optional per-call env argument. Both map forms are
// accepted so direct Go calls and decoded JSON arguments behave the same.
func execEnvFromArgs(args map[string]any) (map[string]string, error) {
	raw, ok := args["env"]
	if !ok || raw == nil {
		return nil, nil
	}

	env := make(map[string]string)
	switch values := raw.(type) {
	case map[string]any:
		for key, value := range values {
			str, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("env value for %q must be a string", key)
			}
			if err := addExecEnv(env, key, str); err != nil {
				return nil, err
			}
		}
	case map[string]string:
		for key, value := range values {
			if err := addExecEnv(env, key, value); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("env must be an object of string values")
	}
	return env, nil
}

// addExecEnv validates one variable before it reaches exec.Cmd.Env.
func addExecEnv(env map[string]string, key, value string) error {
	if key == "" {
		return fmt.Errorf("env variable names must not be empty")
	}
	if strings.ContainsAny(key, "=\x00") {
		return fmt.Errorf("invalid env variable name %q", key)
	}
	if strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("env value for %q contains a NUL byte", key)
	}
	env[key] = value
	return nil
}

// runEnv merges the cached base environment with the per-call LLM variables and
// the trusted PICOCLAW_EXEC_* values, returning a deterministic []string ready
// for exec.Cmd.Env. timeout is the effective foreground timeout; background
// sessions pass 0 because they are not time-limited.
func (t *ExecTool) runEnv(extraEnv map[string]string, timeout time.Duration) []string {
	env := make(map[string]string, len(t.baseEnv)+len(extraEnv)+2)
	for key, value := range t.baseEnv {
		env[execEnvKey(key)] = value
	}
	for key, value := range extraEnv {
		key = execEnvKey(key)
		if isBlockedExecEnv(key) {
			continue
		}
		env[key] = value
	}

	// Set after the LLM merge so these values always describe this run.
	env["PICOCLAW_EXEC_TIME"] = time.Now().Format(time.RFC3339)
	env["PICOCLAW_EXEC_TIMEOUT"] = timeout.String()

	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	logger.DebugCF("shell", "exec environment keys", map[string]any{"keys": keys})

	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+env[key])
	}
	return result
}
