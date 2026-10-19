package toolshared

import (
	"errors"
	"strings"
)

// RequireStringArg returns the trimmed string argument for key. The returned
// error names the argument ("<key> is required") so a tool can turn it into a
// result without spelling the message itself:
//
//	path, err := toolshared.RequireStringArg(args, "path")
//	if err != nil {
//		return ErrorResult(err.Error())
//	}
func RequireStringArg(args map[string]any, key string) (string, error) {
	return requireStringArg(args, key, key+" is required")
}

// RequireNonEmptyStringArg is RequireStringArg with the wording used by the
// spawning and skill tools ("<key> is required and must be a non-empty string").
func RequireNonEmptyStringArg(args map[string]any, key string) (string, error) {
	return requireStringArg(args, key, key+" is required and must be a non-empty string")
}

// requireStringArg trims the argument because padding is always accidental in a
// tool call: " path" and "path " name the same argument value, and a blank
// string is a missing argument, not a value.
func requireStringArg(args map[string]any, key, message string) (string, error) {
	value, _ := args[key].(string)
	if value = strings.TrimSpace(value); value == "" {
		return "", errors.New(message)
	}
	return value, nil
}
