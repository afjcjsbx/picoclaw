package mcp

import (
	"fmt"
	"os"
)

// BuildEnv securely merges the system environment with custom JSON variables.
// Converts the map to a slice []string formatted as "KEY=VALUE",
// which is the format required by os/exec.Cmd.Env.
func BuildEnv(customEnv map[string]string) []string {
	sysEnv := os.Environ()

	if len(customEnv) == 0 {
		return sysEnv
	}

	envMap := make(map[string]string)
	for _, envStr := range sysEnv {
		for i := 0; i < len(envStr); i++ {
			if envStr[i] == '=' {
				envMap[envStr[:i]] = envStr[i+1:]
				break
			}
		}
	}

	for k, v := range customEnv {
		envMap[k] = v
	}

	var finalEnv []string
	for k, v := range envMap {
		finalEnv = append(finalEnv, fmt.Sprintf("%s=%s", k, v))
	}

	return finalEnv
}
