// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/constants"
	"github.com/sipeed/picoclaw/pkg/fileutil"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// A running turn leaves a marker file in <workspace>/state/turns/. A clean end
// removes it; a crash, power loss or shutdown leaves it behind, and
// RecoverInterruptedTurns resumes the turn on the next start. The session
// history is already durable (fsync per message), so the marker only records
// that a turn was in flight and where to answer.

const (
	// ponytail: fixed window and attempt cap; make them config if someone needs it.
	resumeMaxAge      = 6 * time.Hour
	resumeMaxAttempts = 2

	resumeNotice = "[System] The process restarted while you were working on the task above. " +
		"Continue from where you left off. Tool calls that were in flight may or may not have " +
		"completed: check the current state before repeating anything with side effects."
	interruptedToolResult = "No result recorded: the process restarted before this tool call finished. " +
		"It may or may not have run; check the current state before repeating it."
)

type turnMarker struct {
	SessionKey string    `json:"session_key"`
	Channel    string    `json:"channel"`
	ChatID     string    `json:"chat_id"`
	StartedAt  time.Time `json:"started_at"`
	Attempts   int       `json:"attempts"`
}

func turnMarkersDir(workspace string) string {
	return filepath.Join(workspace, "state", "turns")
}

func turnMarkerPath(workspace, sessionKey string) string {
	sum := sha256.Sum256([]byte(sessionKey))
	return filepath.Join(turnMarkersDir(workspace), hex.EncodeToString(sum[:8])+".json")
}

func readTurnMarker(path string) (turnMarker, error) {
	var m turnMarker
	data, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(data, &m)
}

func writeTurnMarker(path string, m turnMarker) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data, 0o600)
}

// resumable reports whether a crash of this turn should be recoverable: a root
// turn with history and a human chat to answer. Cron runs have their own
// delivery bookkeeping, so they are left out.
func (al *AgentLoop) resumable(ts *turnState) bool {
	return al.GetConfig().Agents.Defaults.ResumeInterruptedTurns &&
		ts.parentTurnState == nil &&
		!ts.opts.NoHistory &&
		ts.workspace != "" && ts.channel != "" && ts.chatID != "" &&
		!constants.IsInternalChannel(ts.channel) &&
		!strings.HasPrefix(ts.sessionKey, cronSessionKeyPrefix)
}

// markTurnInFlight records ts as running. A resumed turn keeps the attempt
// count of the marker it replaces, so a task that keeps killing the process
// stops being retried.
func markTurnInFlight(ts *turnState) {
	path := turnMarkerPath(ts.workspace, ts.sessionKey)
	m := turnMarker{SessionKey: ts.sessionKey, Channel: ts.channel, ChatID: ts.chatID, StartedAt: ts.startedAt}
	if old, err := readTurnMarker(path); err == nil && old.SessionKey == ts.sessionKey {
		m.Attempts = old.Attempts
	}
	if err := writeTurnMarker(path, m); err != nil {
		logger.WarnCF("agent", "Failed to write turn marker", map[string]any{
			"session_key": ts.sessionKey,
			"error":       err.Error(),
		})
	}
}

// clearTurnInFlight removes the marker unless the parent context is done: that
// is a shutdown, not an end, and the turn should resume next start. A /stop
// cancels only the turn's own context, so it still clears.
func clearTurnInFlight(ctx context.Context, ts *turnState) {
	if ctx.Err() != nil {
		return
	}
	_ = os.Remove(turnMarkerPath(ts.workspace, ts.sessionKey))
}

// RecoverInterruptedTurns resumes every turn that was running when the previous
// process died. Call it once at startup, after the channels are up.
func (al *AgentLoop) RecoverInterruptedTurns(ctx context.Context) {
	if !al.GetConfig().Agents.Defaults.ResumeInterruptedTurns {
		return
	}
	registry := al.GetRegistry()
	if registry == nil {
		return
	}
	seen := map[string]bool{}
	for _, id := range registry.ListAgentIDs() {
		agent, ok := registry.GetAgent(id)
		if !ok || agent == nil || agent.Workspace == "" || seen[agent.Workspace] {
			continue
		}
		seen[agent.Workspace] = true
		paths, _ := filepath.Glob(filepath.Join(turnMarkersDir(agent.Workspace), "*.json"))
		for _, path := range paths {
			go al.resumeTurn(ctx, path)
		}
	}
}

func (al *AgentLoop) resumeTurn(ctx context.Context, path string) {
	m, err := readTurnMarker(path)
	if err != nil || m.SessionKey == "" {
		_ = os.Remove(path)
		return
	}
	fields := map[string]any{"session_key": m.SessionKey, "attempts": m.Attempts}

	agent := al.agentForSession(m.SessionKey)
	if agent == nil || time.Since(m.StartedAt) > resumeMaxAge || m.Attempts >= resumeMaxAttempts {
		logger.WarnCF("agent", "Dropping interrupted turn marker", fields)
		_ = os.Remove(path)
		return
	}

	// Count the attempt before running: a crash mid-resume must not loop.
	m.Attempts++
	if err := writeTurnMarker(path, m); err != nil {
		logger.WarnCF("agent", "Failed to update turn marker", map[string]any{
			"session_key": m.SessionKey,
			"error":       err.Error(),
		})
	}

	if !closeInterruptedToolCalls(ctx, agent, m.SessionKey) {
		_ = os.Remove(path)
		return
	}

	logger.InfoCF("agent", "Resuming interrupted turn", fields)
	if err := al.enqueueSteeringMessage(
		m.SessionKey, agent.ID, providers.Message{Role: "user", Content: resumeNotice},
	); err != nil {
		return
	}
	target := &continuationTarget{SessionKey: m.SessionKey, Channel: m.Channel, ChatID: m.ChatID}
	response, err := al.drainQueuedSteeringContinuations(ctx, target)
	if err != nil {
		al.maybePublishError(ctx, m.Channel, m.ChatID, m.SessionKey, err)
		return
	}
	al.publishFinalResponse(ctx, m.Channel, m.ChatID, m.SessionKey, response)
}

// closeInterruptedToolCalls gives every tool call of a trailing assistant
// message that has no recorded result a synthetic one, so the history is valid
// for the provider again. It reports false when the turn had in fact finished
// (the last message is a plain assistant reply) and there is nothing to resume.
func closeInterruptedToolCalls(ctx context.Context, agent *AgentInstance, sessionKey string) bool {
	history, err := agent.Sessions.GetHistory(ctx, sessionKey)
	if err != nil || len(history) == 0 {
		return false
	}
	answered := map[string]bool{}
	i := len(history) - 1
	for ; i >= 0 && history[i].Role == "tool"; i-- {
		answered[history[i].ToolCallID] = true
	}
	if i < 0 {
		return false
	}
	last := history[i]
	if last.Role != "assistant" {
		return true
	}
	if len(last.ToolCalls) == 0 {
		return false
	}
	for _, tc := range last.ToolCalls {
		if answered[tc.ID] {
			continue
		}
		warnSessionStoreError(sessionKey, "add full message", agent.Sessions.AddFullMessage(ctx, sessionKey,
			providers.Message{Role: "tool", Content: interruptedToolResult, ToolCallID: tc.ID}))
	}
	warnSessionStoreError(sessionKey, "save", agent.Sessions.Save(ctx, sessionKey))
	return true
}
