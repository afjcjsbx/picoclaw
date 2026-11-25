package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// ErrLocalCommandExecutionDisabled is returned before configured commands are
// started or configured env files are opened without the operator's opt-in.
var ErrLocalCommandExecutionDisabled = errors.New(
	"configured local command execution is disabled; set " + LocalCommandExecutionEnv + "=true to opt in",
)

// RequireLocalCommandExecution checks the environment-only authorization for
// configured MCP stdio commands, process hooks, and MCP env_file loading.
// Missing, empty, invalid, and false values all fail closed.
func RequireLocalCommandExecution() error {
	value, ok := os.LookupEnv(LocalCommandExecutionEnv)
	if !ok {
		return ErrLocalCommandExecutionDisabled
	}

	allowed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil || !allowed {
		return ErrLocalCommandExecutionDisabled
	}
	return nil
}

// LocalCommandExecutionAllowed reports whether the operator explicitly allowed
// configured local commands and MCP env_file loading.
func LocalCommandExecutionAllowed() bool {
	return RequireLocalCommandExecution() == nil
}
