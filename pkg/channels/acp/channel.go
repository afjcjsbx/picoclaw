// Package acp provides the internal channel adapter used by the standalone
// Agent Client Protocol runtime.
package acp

import (
	"context"
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
)

// Observer receives protocol-neutral output from an ACP session.
type Observer interface {
	OnText(ctx context.Context, sessionID, turnID, content string) error
	OnThought(ctx context.Context, sessionID, turnID, content string) error
	OnToolCalls(ctx context.Context, sessionID, turnID, rawCalls string) error
	OnToolResult(ctx context.Context, sessionID, turnID, toolCallID, output string, isError bool) error
	OnFinal(ctx context.Context, sessionID, turnID, content string, msg bus.OutboundMessage) error
}

// Channel adapts PicoClaw channel output to an ACP observer.
type Channel struct {
	*channels.BaseChannel
	observer Observer

	activeMu    sync.RWMutex
	activeTurns map[string]string
}

// New creates an ACP channel adapter.
func New(messageBus *bus.MessageBus, observer Observer) *Channel {
	base := channels.NewBaseChannel("acp", nil, messageBus, []string{"*"})
	channel := &Channel{
		BaseChannel: base,
		observer:    observer,
		activeTurns: make(map[string]string),
	}
	base.SetOwner(channel)
	return channel
}

// Start marks the internal channel as running.
func (c *Channel) Start(context.Context) error {
	c.SetRunning(true)
	return nil
}

// Stop marks the internal channel as stopped.
func (c *Channel) Stop(context.Context) error {
	c.SetRunning(false)
	return nil
}

// SetActiveTurn associates a session with the turn currently being streamed.
func (c *Channel) SetActiveTurn(sessionID, turnID string) {
	c.activeMu.Lock()
	defer c.activeMu.Unlock()
	c.activeTurns[sessionID] = turnID
}

// ClearActiveTurn removes a turn association if it still matches.
func (c *Channel) ClearActiveTurn(sessionID, turnID string) {
	c.activeMu.Lock()
	defer c.activeMu.Unlock()
	if c.activeTurns[sessionID] == turnID {
		delete(c.activeTurns, sessionID)
	}
}

func (c *Channel) activeTurn(sessionID string) string {
	c.activeMu.RLock()
	defer c.activeMu.RUnlock()
	return c.activeTurns[sessionID]
}

// Send translates outbound agent messages into ACP observer callbacks.
func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) ([]string, error) {
	if c.observer == nil {
		return nil, nil
	}
	raw := msg.Context.Raw
	turnID := strings.TrimSpace(raw["acp_turn_id"])
	kind := strings.TrimSpace(raw["message_kind"])
	switch kind {
	case "thought":
		return nil, c.observer.OnThought(ctx, msg.ChatID, turnID, msg.Content)
	case "tool_calls":
		return nil, c.observer.OnToolCalls(ctx, msg.ChatID, turnID, raw["tool_calls"])
	case "tool_result":
		isError := strings.EqualFold(strings.TrimSpace(raw["tool_result_is_error"]), "true")
		return nil, c.observer.OnToolResult(ctx, msg.ChatID, turnID, raw["tool_call_id"], msg.Content, isError)
	case "approval", "tool_feedback":
		return nil, nil
	}
	if strings.EqualFold(strings.TrimSpace(raw["outbound_kind"]), "final") {
		return nil, c.observer.OnFinal(ctx, msg.ChatID, turnID, msg.Content, msg)
	}
	return nil, c.observer.OnText(ctx, msg.ChatID, turnID, msg.Content)
}

// BeginStream returns an append-only ACP streamer. AgentLoop passes cumulative
// content, so the streamer emits only newly appended text.
func (c *Channel) BeginStream(_ context.Context, chatID string) (channels.Streamer, error) {
	return &streamer{observer: c.observer, sessionID: chatID, turnID: c.activeTurn(chatID)}, nil
}

// SupportsStructuredTurnOutput marks ACP as a channel that consumes structured
// thought and tool activity instead of plain tool-feedback messages.
func (c *Channel) SupportsStructuredTurnOutput() bool {
	return true
}

type streamer struct {
	observer  Observer
	sessionID string
	turnID    string

	mu        sync.Mutex
	content   string
	reasoning string
	modelName string
	turnUsage *bus.TokenUsage
	finalized bool
}

func (s *streamer) Update(ctx context.Context, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finalized {
		return nil
	}
	delta := appendedDelta(s.content, content)
	s.content = content
	if delta == "" || s.observer == nil {
		return nil
	}
	return s.observer.OnText(ctx, s.sessionID, s.turnID, delta)
}

func (s *streamer) Finalize(ctx context.Context, content string) error {
	return s.FinalizeWithContext(ctx, content, nil)
}

func (s *streamer) FinalizeWithContext(ctx context.Context, content string, usage *bus.ContextUsage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finalized {
		return nil
	}
	s.finalized = true
	delta := appendedDelta(s.content, content)
	s.content = content
	if delta != "" && s.observer != nil {
		if err := s.observer.OnText(ctx, s.sessionID, s.turnID, delta); err != nil {
			return err
		}
	}
	if s.observer == nil {
		return nil
	}
	msg := bus.OutboundMessage{Content: content, ContextUsage: usage}
	msg.Context.Raw = map[string]string{"outbound_kind": "final"}
	if s.modelName != "" {
		msg.Context.Raw["model_name"] = s.modelName
	}
	if s.turnUsage != nil {
		value := *s.turnUsage
		msg.Usage = &value
	}
	return s.observer.OnFinal(ctx, s.sessionID, s.turnID, content, msg)
}

func (s *streamer) Cancel(context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalized = true
}

func (s *streamer) UpdateReasoning(ctx context.Context, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finalized {
		return nil
	}
	delta := appendedDelta(s.reasoning, content)
	s.reasoning = content
	if delta == "" || s.observer == nil {
		return nil
	}
	return s.observer.OnThought(ctx, s.sessionID, s.turnID, delta)
}

func (s *streamer) FinalizeReasoning(ctx context.Context, content string) error {
	return s.UpdateReasoning(ctx, content)
}

func (s *streamer) SetModelName(modelName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modelName = strings.TrimSpace(modelName)
}

func (s *streamer) SetTurnUsage(usage bus.TokenUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := usage
	s.turnUsage = &value
}

func appendedDelta(previous, current string) string {
	if current == "" {
		return ""
	}
	if previous == "" {
		return current
	}
	if strings.HasPrefix(current, previous) {
		return strings.TrimPrefix(current, previous)
	}
	return current
}
