package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// limitWriter is a helper struct that caps the amount of data written to an underlying buffer.
type limitWriter struct {
	buf       *bytes.Buffer
	limit     int
	truncated bool
}

func (w *limitWriter) Write(p []byte) (int, error) {
	// If the buffer is already at or past the limit, we "successfully" discard the data
	// to prevent the external command from failing with a "broken pipe" error.
	if w.buf.Len() >= w.limit {
		w.truncated = true
		return 0, fmt.Errorf("output limit reached") // Breaks the pipe
	}

	remaining := w.limit - w.buf.Len()
	toWrite := len(p)
	if toWrite > remaining {
		toWrite = remaining
		w.truncated = true
	}

	return w.buf.Write(p[:toWrite])
}

// helper to determine whether the output contains binary data
func isBinary(data []byte) bool {
	return bytes.Contains(data, []byte{0})
}

type ADBTool struct {
	mu      sync.RWMutex
	timeout time.Duration
}

func NewADBTool() *ADBTool {
	return &ADBTool{
		timeout: 60 * time.Second, // Default timeout to avoid blockages
	}
}

func (t *ADBTool) Name() string {
	return "adb"
}

func (t *ADBTool) Description() string {
	return "Runs Android Debug Bridge (adb) commands to communicate with a connected Android device. Allows you to use the device shell, install apps, download/upload files, or read the logcat."
}

func (t *ADBTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"args": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "string",
				},
				"description": "The arguments to be passed to adb as a list of strings (e.g. ['shell', 'ls', '-l', '/sdcard'], ['devices'], ['logcat', '-d'])",
			},
			"device_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional. Specifies target device ID (adds flag -s <device_id> automatically).",
			},
		},
		"required": []string{"args"},
	}
}

var (
	// Top-level ADB subcommands allowed
	allowedAdbSubcommands = map[string]bool{
		"shell":   true,
		"logcat":  true,
		"devices": true,
		"install": true,
		"push":    true, // Usare con cautela
		"pull":    true,
	}

	// Commands inside "adb shell" considered safe
	allowedShellCommands = map[string]bool{
		"input":     true, // Per tap, swipe, text
		"am":        true, // Activity Manager (start app)
		"pm":        true, // Package Manager (list packages)
		"wm":        true, // Window Manager (size, density)
		"getprop":   true, // Reading properties
		"dumpsys":   true, // System status
		"ls":        true,
		"echo":      true,
		"screencap": true,
	}

	// Patterns categorically prohibited
	dangerousPatterns = []string{
		`rm\s+`, `format`, `mkfs`, `dd\s+`, `> /`, `chmod`, `chown`, `reboot`, `shutdown`,
	}
)

func (t *ADBTool) guardArguments(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no arguments provided")
	}

	subcommand := args[0]
	if !allowedAdbSubcommands[subcommand] {
		return fmt.Errorf("adb subcommand %q is not allowed for security reasons", subcommand)
	}

	// If the command is 'shell', we inspect the next command
	if subcommand == "shell" && len(args) > 1 {
		if len(args) < 2 {
			return fmt.Errorf("interactive 'adb shell' is not supported; provide a shell command")
		}

		shellCmd := args[1]
		if !allowedShellCommands[shellCmd] {
			return fmt.Errorf("shell command %q is restricted", shellCmd)
		}

		// Sanitization control versus injection of shell operators (;, &&, |, etc.)
		fullShellString := strings.Join(args[1:], " ")
		if strings.ContainsAny(fullShellString, ";&|><`$\n\r") {
			return fmt.Errorf("shell operators or subshells are not allowed in adb arguments")
		}

		// Hazardous pattern control
		lowerCmd := strings.ToLower(fullShellString)
		for _, pattern := range dangerousPatterns {
			matched, _ := regexp.MatchString(pattern, lowerCmd)
			if matched {
				return fmt.Errorf("dangerous pattern detected in shell command")
			}
		}
	}

	if subcommand == "logcat" {
		hasDumpFlag := false
		for _, a := range args {
			if a == "-d" {
				hasDumpFlag = true
				break
			}
		}
		if !hasDumpFlag {
			args = append(args, "-d")
		}
	}

	return nil
}

func (t *ADBTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	rawArgs, ok := args["args"].([]interface{})
	if !ok || len(rawArgs) == 0 {
		msg := "The parameter 'args' is mandatory and must be an array of strings."
		return &ToolResult{
			ForLLM:  msg,
			ForUser: msg,
			IsError: true,
		}
	}

	var adbArgs []string

	for _, arg := range rawArgs {
		adbArgs = append(adbArgs, fmt.Sprintf("%v", arg))
	}

	attemptedCmd := strings.Join(adbArgs, " ")
	// Security checks
	if err := t.guardArguments(adbArgs); err != nil {
		fmt.Printf("Locked unsafe command: adb %s\\nMotive:%v\n", attemptedCmd, err)
		return &ToolResult{
			ForLLM:  fmt.Sprintf("Security Error: %v. Attempted command was: adb %s", err, attemptedCmd),
			ForUser: fmt.Sprintf("Action blocked for security reasons.\\nCommand attempted: `adb %s`", attemptedCmd),
			IsError: true,
		}
	}

	// If a device_id is provided, we inject the -s flag
	var finalArgs []string
	if deviceID, ok := args["device_id"].(string); ok && deviceID != "" {
		finalArgs = append(finalArgs, "-s", deviceID)
	}
	finalArgs = append(finalArgs, adbArgs...)

	// Configure context and timeout (using the mutex to safely read t.timeout)
	t.mu.RLock()
	timeout := t.timeout
	t.mu.RUnlock()

	var cmdCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "adb", finalArgs...)

	// Use limitWriter to bound memory usage to ~15KB per stream
	var stdoutBuf, stderrBuf bytes.Buffer
	const maxStreamSize = 15000
	stdoutWriter := limitWriter{buf: &stdoutBuf, limit: maxStreamSize}
	stderrWriter := limitWriter{buf: &stderrBuf, limit: maxStreamSize}
	cmd.Stdout = &stdoutWriter
	cmd.Stderr = &stderrWriter

	err := cmd.Run()

	if isBinary(stdoutBuf.Bytes()) || isBinary(stderrBuf.Bytes()) {
		msg := "Binary data detected in output (e.g. an image or file). Output suppressed to protect text context."
		return &ToolResult{
			ForLLM:  msg,
			ForUser: "The data received from the device were in binary format and were ignored.",
			IsError: true,
		}
	}

	output := stdoutBuf.String()

	if stdoutWriter.truncated {
		output += "\n... [WARNING: STDOUT TRUNCATED BY SYSTEM TO PREVENT MEMORY OVERFLOW] ..."
	}

	if stderrBuf.Len() > 0 {
		output += "\nSTDERR:\n" + stderrBuf.String()
		if stderrWriter.truncated {
			output += "\n... [WARNING: STDERR TRUNCATED BY SYSTEM TO PREVENT MEMORY OVERFLOW] ..."
		}
	}

	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			msg := "CRITICAL SYSTEM ERROR: The 'adb' executable was not found in the system $PATH. Please inform the system administrator to install Android Platform Tools."
			return &ToolResult{
				ForLLM:  msg,
				ForUser: msg,
				IsError: true,
			}
		}

		if errors.Is(cmdCtx.Err(), context.Canceled) {
			msg := "ADB command was canceled by the system."
			return &ToolResult{ForLLM: msg, ForUser: msg, IsError: true}
		}

		// Optional: Re-add the DeadlineExceeded check for timeouts
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			output += fmt.Sprintf("\n[WARNING: ADB Command timed out after %v]", timeout)
			msg := fmt.Sprintf("ADB command timed out after %v", timeout)
			return &ToolResult{ForLLM: msg, ForUser: msg, IsError: true}
		}

		output += fmt.Sprintf("\nExit code: %v", err)
	}

	if output == "" {
		output = "(no output)"
	}

	// Truncate output so as not to exceed the LLM's context limits
	maxLen := 10000
	runes := []rune(output)
	if len(runes) > maxLen {
		output = string(runes[:maxLen]) + fmt.Sprintf("\n... (truncated, %d characters remaining)", len(runes)-maxLen)
	}

	return &ToolResult{
		ForLLM:  output,
		ForUser: output,
		IsError: err != nil,
	}
}

func (t *ADBTool) SetTimeout(timeout time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.timeout = timeout
}
