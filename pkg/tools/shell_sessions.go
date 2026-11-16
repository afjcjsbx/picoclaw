package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

func (t *ExecTool) executeList() *ToolResult {
	sessions := t.sessionManager.List()
	resp := ExecResponse{
		Sessions: sessions,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: fmt.Sprintf("%d active sessions", len(sessions)),
		IsError: false,
	}
}

// sessionIDArg extracts the required sessionId argument.
func sessionIDArg(args map[string]any) (string, *ToolResult) {
	sessionID, ok := args["sessionId"].(string)
	if !ok {
		return "", ErrorResult("sessionId is required")
	}
	return sessionID, nil
}

func (t *ExecTool) lookupSession(sessionID string) (*ProcessSession, *ToolResult) {
	session, err := t.sessionManager.Get(sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return nil, ErrorResult(fmt.Sprintf("session not found: %s", sessionID))
		}
		return nil, ErrorResult(err.Error())
	}
	return session, nil
}

// getSession resolves the sessionId argument to a live session. Actions that
// validate other arguments before the lookup call sessionIDArg and
// lookupSession separately to keep their error order.
func (t *ExecTool) getSession(args map[string]any) (string, *ProcessSession, *ToolResult) {
	sessionID, errResult := sessionIDArg(args)
	if errResult != nil {
		return "", nil, errResult
	}
	session, errResult := t.lookupSession(sessionID)
	return sessionID, session, errResult
}

func (t *ExecTool) executePoll(args map[string]any) *ToolResult {
	sessionID, session, errResult := t.getSession(args)
	if errResult != nil {
		return errResult
	}

	resp := ExecResponse{
		SessionID:    sessionID,
		Status:       session.GetStatus(),
		ExitCode:     sessionExitCode(session),
		Output:       session.Tail(pollOutputChars),
		TotalBytes:   session.TotalBytes(),
		DroppedBytes: session.DroppedBytes(),
	}
	if !session.IsDone() && session.noteCheck() >= idleCheckLimit {
		resp.Note = pollIdleNote
		if session.StartNotify() || session.NotifyEnabled() {
			resp.Note += " " + autoNotifyNote
		}
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return &ToolResult{
		ForLLM:  string(data),
		IsError: false,
	}
}

const (
	// idleCheckLimit is how many consecutive poll/wait calls on a silent running
	// session it takes to tell the model to stop checking.
	idleCheckLimit = 2
	pollIdleNote   = "Still running with no new output since your last checks. poll returns " +
		"immediately, so repeating it will not help: use action=wait (with timeout) to block " +
		"until it exits, or do other work."
	waitIdleNote = "No new output after repeated waits. If that is not expected, read the " +
		"output or kill the session instead of waiting again."
	// defaultWaitMax caps one wait when no exec timeout is configured.
	defaultWaitMax = 60 * time.Second
	// completionOutputChars bounds the output tail in a completion notice.
	completionOutputChars = 1000
	pollOutputChars       = 1000
	waitOutputChars       = 1000
	killOutputChars       = 1000

	notifyEnabledNote = "You will be notified when this session exits; do not poll or wait in a loop."
	autoNotifyNote    = "A completion notification is enabled; the result will arrive as a follow-up message."

	// timeoutBackgroundGuidance is appended to a timed-out foreground result.
	// A server, daemon, or watch process should be a tracked background session
	// instead of a foreground call that blocks until the timeout kills it.
	timeoutBackgroundGuidance = "If this is a server, daemon, or watch process, re-run it with background=true " +
		"and use the returned sessionId with wait, poll, read, or kill instead of blocking a foreground call."
)

// executeWait blocks until the session exits, the timeout elapses, or ctx is
// canceled. A wait that times out is not a failure: the response says so, since
// models otherwise misread it and re-issue the same call.
func (t *ExecTool) executeWait(ctx context.Context, args map[string]any) *ToolResult {
	sessionID, errResult := sessionIDArg(args)
	if errResult != nil {
		return errResult
	}

	maxWait := t.timeout
	if maxWait <= 0 {
		maxWait = defaultWaitMax
	}
	window, clampNote := maxWait, ""
	if raw, ok := args["timeout"]; ok {
		// JSON tool arguments always decode numbers as float64.
		value, isNumber := raw.(float64)
		if !isNumber || value != math.Trunc(value) || value < 1 {
			return ErrorResult("timeout must be a positive integer number of seconds")
		}
		if requested := time.Duration(value) * time.Second; requested <= maxWait {
			window = requested
		} else {
			clampNote = fmt.Sprintf("Requested wait of %ds was clamped to the configured limit of %ds. ",
				int64(value), int64(maxWait/time.Second))
		}
	}

	session, errResult := t.lookupSession(sessionID)
	if errResult != nil {
		return errResult
	}

	// ponytail: polls IsDone every 100ms instead of a done channel; a queued
	// steering message does not release the wait, only ctx cancellation does.
	deadline := time.NewTimer(window)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	timedOut := false
	for !timedOut && !session.IsDone() {
		select {
		case <-ctx.Done():
			return ErrorResult(fmt.Sprintf("wait interrupted: %v", ctx.Err()))
		case <-deadline.C:
			timedOut = true
		case <-tick.C:
		}
	}

	resp := ExecResponse{
		SessionID:    sessionID,
		Status:       session.GetStatus(),
		ExitCode:     sessionExitCode(session),
		TotalBytes:   session.TotalBytes(),
		DroppedBytes: session.DroppedBytes(),
	}
	if session.IsDone() {
		resp.Output = session.Read()
	} else {
		resp.Output = session.Tail(waitOutputChars)
		resp.Note = clampNote + fmt.Sprintf(
			"Wait window of %ds elapsed. The process is still running; this is not an error.",
			int64(window/time.Second))
		if session.noteCheck() >= idleCheckLimit {
			resp.Note += " " + waitIdleNote
			if session.StartNotify() || session.NotifyEnabled() {
				resp.Note += " " + autoNotifyNote
			}
		}
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return &ToolResult{ForLLM: string(data)}
}

func (t *ExecTool) executeRead(args map[string]any) *ToolResult {
	sessionID, session, errResult := t.getSession(args)
	if errResult != nil {
		return errResult
	}

	offset := int64(0)
	offsetProvided := false
	if raw, ok := args["offset"]; ok {
		value, isNumber := raw.(float64)
		if !isNumber || value != math.Trunc(value) || value < 0 {
			return ErrorResult("offset must be a non-negative integer number of bytes")
		}
		offset = int64(value)
		offsetProvided = true
	}

	limit := int64(t.maxOutputChars)
	if limit <= 0 {
		limit = config.DefaultExecMaxOutputChars
	}
	if raw, ok := args["limit"]; ok {
		value, isNumber := raw.(float64)
		if !isNumber || value != math.Trunc(value) || value < 1 {
			return ErrorResult("limit must be a positive integer number of bytes")
		}
		limit = int64(value)
	}
	if !offsetProvided {
		if total := session.TotalBytes(); total > limit {
			offset = total - limit
		}
	}

	page := session.ReadRange(offset, limit)
	resp := ExecResponse{
		SessionID:    sessionID,
		Output:       page.Text,
		Status:       session.GetStatus(),
		ExitCode:     sessionExitCode(session),
		TotalBytes:   page.TotalBytes,
		Offset:       page.Offset,
		NextOffset:   page.NextOffset,
		HasMore:      page.HasMore,
		DroppedBytes: page.DroppedBytes,
		SkippedBytes: page.SkippedBytes,
	}
	if page.SkippedBytes > 0 {
		resp.Note = fmt.Sprintf(
			"Requested offset %d predates the retained output window; skipped %d bytes, starting at byte %d.",
			offset, page.SkippedBytes, page.Offset,
		)
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return &ToolResult{
		ForLLM:  string(data),
		IsError: false,
	}
}

func (t *ExecTool) executeWrite(args map[string]any) *ToolResult {
	sessionID, errResult := sessionIDArg(args)
	if errResult != nil {
		return errResult
	}

	data, ok := args["data"].(string)
	if !ok {
		return ErrorResult("data is required")
	}

	session, errResult := t.lookupSession(sessionID)
	if errResult != nil {
		return errResult
	}

	if session.IsDone() {
		return ErrorResult(fmt.Sprintf("process already exited with code %d", session.GetExitCode()))
	}

	if err := session.Write(data); err != nil {
		if errors.Is(err, ErrSessionDone) {
			return ErrorResult(fmt.Sprintf("process already exited with code %d", session.GetExitCode()))
		}
		return ErrorResult(fmt.Sprintf("failed to write to session: %v", err))
	}

	resp := ExecResponse{
		SessionID: sessionID,
		Status:    session.GetStatus(),
	}
	respData, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return &ToolResult{
		ForLLM:  string(respData),
		IsError: false,
	}
}

func (t *ExecTool) executeKill(args map[string]any) *ToolResult {
	sessionID, session, errResult := t.getSession(args)
	if errResult != nil {
		return errResult
	}

	if session.IsDone() {
		return ErrorResult(fmt.Sprintf("process already exited with code %d", session.GetExitCode()))
	}

	if err := session.Kill(); err != nil {
		return ErrorResult(fmt.Sprintf("failed to kill session: %v", err))
	}

	resp := ExecResponse{
		SessionID:    sessionID,
		Status:       session.GetStatus(),
		ExitCode:     sessionExitCode(session),
		Output:       session.Tail(killOutputChars),
		TotalBytes:   session.TotalBytes(),
		DroppedBytes: session.DroppedBytes(),
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: fmt.Sprintf("Session %s killed; output retained for 30 minutes", sessionID),
		IsError: false,
	}
}

// keyMap maps key names to their escape sequences.
var keyMap = map[string]string{
	"enter":     "\r",
	"return":    "\r",
	"tab":       "\t",
	"escape":    "\x1b",
	"esc":       "\x1b",
	"space":     " ",
	"backspace": "\x7f",
	"bspace":    "\x7f",
	"up":        "\x1b[A",
	"down":      "\x1b[B",
	"right":     "\x1b[C",
	"left":      "\x1b[D",
	"home":      "\x1b[1~",
	"end":       "\x1b[4~",
	"pageup":    "\x1b[5~",
	"pagedown":  "\x1b[6~",
	"pgup":      "\x1b[5~",
	"pgdn":      "\x1b[6~",
	"insert":    "\x1b[2~",
	"ic":        "\x1b[2~",
	"delete":    "\x1b[3~",
	"del":       "\x1b[3~",
	"dc":        "\x1b[3~",
	"btab":      "\x1b[Z",
	"f1":        "\x1bOP",
	"f2":        "\x1bOQ",
	"f3":        "\x1bOR",
	"f4":        "\x1bOS",
	"f5":        "\x1b[15~",
	"f6":        "\x1b[17~",
	"f7":        "\x1b[18~",
	"f8":        "\x1b[19~",
	"f9":        "\x1b[20~",
	"f10":       "\x1b[21~",
	"f11":       "\x1b[23~",
	"f12":       "\x1b[24~",
}

// ss3KeysMap maps key names to SS3 escape sequences
var ss3KeysMap = map[string]string{
	"up":    "\x1bOA",
	"down":  "\x1bOB",
	"right": "\x1bOC",
	"left":  "\x1bOD",
	"home":  "\x1bOH",
	"end":   "\x1bOF",
}

func detectPtyKeyMode(raw string) PtyKeyMode {
	const SMKX = "\x1b[?1h"
	const RMKX = "\x1b[?1l"

	lastSmkx := strings.LastIndex(raw, SMKX)
	lastRmkx := strings.LastIndex(raw, RMKX)

	if lastSmkx == -1 && lastRmkx == -1 {
		return PtyKeyModeNotFound
	}

	if lastSmkx > lastRmkx {
		return PtyKeyModeSS3
	}
	return PtyKeyModeCSI
}

// encodeKeyToken encodes a single key token into its escape sequence.
// Supports:
//   - Named keys: "enter", "tab", "up", "ctrl-c", "alt-x", etc.
//   - Ctrl modifier: "ctrl-c" or "c-c" (sends Ctrl+char)
//   - Alt modifier: "alt-x" or "m-x" (sends ESC+char)
func encodeKeyToken(token string, ptyKeyMode PtyKeyMode) (string, error) {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return "", nil
	}

	// Handle ctrl-X format (c-x)
	if strings.HasPrefix(token, "c-") {
		char := token[2]
		if char >= 'a' && char <= 'z' {
			return string(rune(char) & 0x1f), nil // ctrl-a through ctrl-z
		}
		return "", fmt.Errorf("invalid ctrl key: %s", token)
	}

	// Handle ctrl-X format (ctrl-x)
	if strings.HasPrefix(token, "ctrl-") {
		char := token[5]
		if char >= 'a' && char <= 'z' {
			return string(rune(char) & 0x1f), nil
		}
		return "", fmt.Errorf("invalid ctrl key: %s", token)
	}

	// Handle alt-X format (m-x or alt-x)
	if strings.HasPrefix(token, "m-") || strings.HasPrefix(token, "alt-") {
		var char string
		if strings.HasPrefix(token, "m-") {
			char = token[2:]
		} else {
			char = token[4:]
		}
		if len(char) == 1 {
			return "\x1b" + char, nil
		}
		return "", fmt.Errorf("invalid alt key: %s", token)
	}

	// Handle shift modifier for special keys (shift-up, shift-down, etc.)
	if strings.HasPrefix(token, "s-") || strings.HasPrefix(token, "shift-") {
		var key string
		if strings.HasPrefix(token, "s-") {
			key = token[2:]
		} else {
			key = token[6:]
		}
		// Apply shift modifier: for single-char keys, return uppercase
		if seq, ok := keyMap[key]; ok {
			// For escape sequences, we can't easily add shift
			// For single-char keys (letters), return uppercase
			if len(seq) == 1 {
				return strings.ToUpper(seq), nil
			}
			return seq, nil
		}
		return "", fmt.Errorf("unknown key with shift: %s", key)
	}

	if ptyKeyMode == PtyKeyModeSS3 {
		if seq, ok := ss3KeysMap[token]; ok {
			return seq, nil
		}
	}

	if seq, ok := keyMap[token]; ok {
		return seq, nil
	}

	return "", fmt.Errorf("unknown key: %s (use write action for text input)", token)
}

// encodeKeySequence encodes a slice of key tokens into a single string.
func encodeKeySequence(tokens []string, ptyKeyMode PtyKeyMode) (string, error) {
	var result string
	for _, token := range tokens {
		seq, err := encodeKeyToken(token, ptyKeyMode)
		if err != nil {
			return "", err
		}
		result += seq
	}
	return result, nil
}

func (t *ExecTool) executeSendKeys(args map[string]any) *ToolResult {
	sessionID, errResult := sessionIDArg(args)
	if errResult != nil {
		return errResult
	}

	keysStr, ok := args["keys"].(string)
	if !ok {
		return ErrorResult("keys must be a string")
	}

	if keysStr == "" {
		return ErrorResult("keys cannot be empty")
	}

	// Parse comma-separated key names
	keyNames := strings.Split(keysStr, ",")
	var keys []string
	for _, k := range keyNames {
		k = strings.TrimSpace(k)
		if k != "" {
			keys = append(keys, k)
		}
	}

	if len(keys) == 0 {
		return ErrorResult("keys cannot be empty")
	}

	session, errResult := t.lookupSession(sessionID)
	if errResult != nil {
		return errResult
	}

	ptyKeyMode := session.GetPtyKeyMode()

	data, err := encodeKeySequence(keys, ptyKeyMode)
	if err != nil {
		return ErrorResult(fmt.Sprintf("invalid key: %v", err))
	}

	if session.IsDone() {
		return ErrorResult(fmt.Sprintf("process already exited with code %d", session.GetExitCode()))
	}

	if err = session.Write(data); err != nil {
		if errors.Is(err, ErrSessionDone) {
			return ErrorResult(fmt.Sprintf("process already exited with code %d", session.GetExitCode()))
		}
		return ErrorResult(fmt.Sprintf("failed to send keys: %v", err))
	}

	resp := ExecResponse{
		SessionID: sessionID,
		Status:    "running",
		Output:    fmt.Sprintf("Sent keys: %v", keys),
	}
	respData, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return &ToolResult{
		ForLLM:  string(respData),
		IsError: false,
	}
}
