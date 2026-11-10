package channels

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestBaseChannelIsAllowed(t *testing.T) {
	tests := []struct {
		name      string
		allowList []string
		senderID  string
		want      bool
	}{
		{
			name:      "empty allowlist allows all",
			allowList: nil,
			senderID:  "anyone",
			want:      true,
		},
		{
			name:      "compound sender matches numeric allowlist",
			allowList: []string{"123456"},
			senderID:  "123456|alice",
			want:      true,
		},
		{
			name:      "compound sender matches username allowlist",
			allowList: []string{"@alice"},
			senderID:  "123456|alice",
			want:      true,
		},
		{
			name:      "numeric sender matches legacy compound allowlist",
			allowList: []string{"123456|alice"},
			senderID:  "123456",
			want:      true,
		},
		{
			name:      "non matching sender is denied",
			allowList: []string{"123456"},
			senderID:  "654321|bob",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := NewBaseChannel("test", nil, nil, tt.allowList)
			if got := ch.IsAllowed(tt.senderID); got != tt.want {
				t.Fatalf("IsAllowed(%q) = %v, want %v", tt.senderID, got, tt.want)
			}
		})
	}
}

func TestShouldRespondInGroup(t *testing.T) {
	tests := []struct {
		name        string
		gt          config.GroupTriggerConfig
		isMentioned bool
		content     string
		wantRespond bool
		wantContent string
	}{
		{
			name:        "no config - permissive default",
			gt:          config.GroupTriggerConfig{},
			isMentioned: false,
			content:     "hello world",
			wantRespond: true,
			wantContent: "hello world",
		},
		{
			name:        "no config - mentioned",
			gt:          config.GroupTriggerConfig{},
			isMentioned: true,
			content:     "hello world",
			wantRespond: true,
			wantContent: "hello world",
		},
		{
			name:        "mention_only - not mentioned",
			gt:          config.GroupTriggerConfig{MentionOnly: true},
			isMentioned: false,
			content:     "hello world",
			wantRespond: false,
			wantContent: "hello world",
		},
		{
			name:        "mention_only - mentioned",
			gt:          config.GroupTriggerConfig{MentionOnly: true},
			isMentioned: true,
			content:     "hello world",
			wantRespond: true,
			wantContent: "hello world",
		},
		{
			name:        "prefix match",
			gt:          config.GroupTriggerConfig{Prefixes: []string{"/ask"}},
			isMentioned: false,
			content:     "/ask hello",
			wantRespond: true,
			wantContent: "hello",
		},
		{
			name:        "prefix no match - not mentioned",
			gt:          config.GroupTriggerConfig{Prefixes: []string{"/ask"}},
			isMentioned: false,
			content:     "hello world",
			wantRespond: false,
			wantContent: "hello world",
		},
		{
			name:        "prefix no match - but mentioned",
			gt:          config.GroupTriggerConfig{Prefixes: []string{"/ask"}},
			isMentioned: true,
			content:     "hello world",
			wantRespond: true,
			wantContent: "hello world",
		},
		{
			name:        "multiple prefixes - second matches",
			gt:          config.GroupTriggerConfig{Prefixes: []string{"/ask", "/bot"}},
			isMentioned: false,
			content:     "/bot help me",
			wantRespond: true,
			wantContent: "help me",
		},
		{
			name:        "mention_only with prefixes - mentioned overrides",
			gt:          config.GroupTriggerConfig{MentionOnly: true, Prefixes: []string{"/ask"}},
			isMentioned: true,
			content:     "hello",
			wantRespond: true,
			wantContent: "hello",
		},
		{
			name:        "mention_only with prefixes - not mentioned, no prefix",
			gt:          config.GroupTriggerConfig{MentionOnly: true, Prefixes: []string{"/ask"}},
			isMentioned: false,
			content:     "hello",
			wantRespond: false,
			wantContent: "hello",
		},
		{
			name:        "empty prefix in list is skipped",
			gt:          config.GroupTriggerConfig{Prefixes: []string{"", "/ask"}},
			isMentioned: false,
			content:     "/ask test",
			wantRespond: true,
			wantContent: "test",
		},
		{
			name:        "prefix strips leading whitespace after prefix",
			gt:          config.GroupTriggerConfig{Prefixes: []string{"/ask "}},
			isMentioned: false,
			content:     "/ask hello",
			wantRespond: true,
			wantContent: "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := NewBaseChannel("test", nil, nil, nil, WithGroupTrigger(tt.gt))
			gotRespond, gotContent := ch.ShouldRespondInGroup(tt.isMentioned, tt.content)
			if gotRespond != tt.wantRespond {
				t.Errorf("ShouldRespondInGroup() respond = %v, want %v", gotRespond, tt.wantRespond)
			}
			if gotContent != tt.wantContent {
				t.Errorf("ShouldRespondInGroup() content = %q, want %q", gotContent, tt.wantContent)
			}
		})
	}
}

func TestIsAllowedSender(t *testing.T) {
	tests := []struct {
		name      string
		allowList []string
		sender    bus.SenderInfo
		want      bool
	}{
		{
			name:      "empty allowlist allows all",
			allowList: nil,
			sender:    bus.SenderInfo{PlatformID: "anyone"},
			want:      true,
		},
		{
			name:      "numeric ID matches PlatformID",
			allowList: []string{"123456"},
			sender: bus.SenderInfo{
				Platform:    "telegram",
				PlatformID:  "123456",
				CanonicalID: "telegram:123456",
			},
			want: true,
		},
		{
			name:      "canonical format matches",
			allowList: []string{"telegram:123456"},
			sender: bus.SenderInfo{
				Platform:    "telegram",
				PlatformID:  "123456",
				CanonicalID: "telegram:123456",
			},
			want: true,
		},
		{
			name:      "canonical format wrong platform",
			allowList: []string{"discord:123456"},
			sender: bus.SenderInfo{
				Platform:    "telegram",
				PlatformID:  "123456",
				CanonicalID: "telegram:123456",
			},
			want: false,
		},
		{
			name:      "@username matches",
			allowList: []string{"@alice"},
			sender: bus.SenderInfo{
				Platform:    "telegram",
				PlatformID:  "123456",
				CanonicalID: "telegram:123456",
				Username:    "alice",
			},
			want: true,
		},
		{
			name:      "compound id|username matches by ID",
			allowList: []string{"123456|alice"},
			sender: bus.SenderInfo{
				Platform:    "telegram",
				PlatformID:  "123456",
				CanonicalID: "telegram:123456",
				Username:    "alice",
			},
			want: true,
		},
		{
			name:      "non matching sender denied",
			allowList: []string{"654321"},
			sender: bus.SenderInfo{
				Platform:    "telegram",
				PlatformID:  "123456",
				CanonicalID: "telegram:123456",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := NewBaseChannel("test", nil, nil, tt.allowList)
			if got := ch.IsAllowedSender(tt.sender); got != tt.want {
				t.Fatalf("IsAllowedSender(%+v) = %v, want %v", tt.sender, got, tt.want)
			}
		})
	}
}

func TestHandleInboundContext_PublishesNormalizedContext(t *testing.T) {
	tests := []struct {
		name       string
		inbound    bus.InboundContext
		wantChat   string
		wantSender string
	}{
		{
			name: "direct uses sender as peer",
			inbound: bus.InboundContext{
				Channel:   "test",
				ChatID:    "chat-1",
				ChatType:  "direct",
				SenderID:  "user-1",
				MessageID: "msg-1",
			},
			wantChat:   "chat-1",
			wantSender: "user-1",
		},
		{
			name: "group uses chat as peer",
			inbound: bus.InboundContext{
				Channel:   "test",
				ChatID:    "group-1",
				ChatType:  "group",
				SenderID:  "user-2",
				MessageID: "msg-2",
			},
			wantChat:   "group-1",
			wantSender: "user-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgBus := bus.NewMessageBus()
			defer msgBus.Close()

			ch := NewBaseChannel("test", nil, msgBus, nil)
			ch.HandleInboundContext(context.Background(), tt.inbound.ChatID, "hello", nil, tt.inbound)

			msg := <-msgBus.InboundChan()
			if msg.ChatID != tt.wantChat {
				t.Fatalf("ChatID = %q, want %q", msg.ChatID, tt.wantChat)
			}
			if msg.SenderID != tt.wantSender {
				t.Fatalf("SenderID = %q, want %q", msg.SenderID, tt.wantSender)
			}
			if msg.Context.ChatType != tt.inbound.ChatType {
				t.Fatalf("ChatType = %q, want %q", msg.Context.ChatType, tt.inbound.ChatType)
			}
		})
	}
}

// fakePlaceholderRecorder captures the capability callbacks registered by
// BaseChannel.HandleMessageWithContext so tests can assert the inbound
// orchestration without a real Manager.
type fakePlaceholderRecorder struct {
	typingStops   int
	reactionUndos int
	placeholders  int
}

func (f *fakePlaceholderRecorder) RecordPlaceholder(string, string, string) { f.placeholders++ }
func (f *fakePlaceholderRecorder) RecordTypingStop(string, string, func())  { f.typingStops++ }
func (f *fakePlaceholderRecorder) RecordReactionUndo(string, string, func()) {
	f.reactionUndos++
}

// reactionCapableChannel is a minimal channel that only advertises
// ReactionCapable, used to prove BaseChannel auto-registers the undo.
type reactionCapableChannel struct {
	*BaseChannel
	reactions int
	undos     int
	lastMsgID string
}

func (c *reactionCapableChannel) ReactToMessage(_ context.Context, _, messageID string) (func(), error) {
	c.reactions++
	c.lastMsgID = messageID
	return func() { c.undos++ }, nil
}

func (c *reactionCapableChannel) Send(context.Context, bus.OutboundMessage) ([]string, error) {
	return nil, nil
}

func (c *reactionCapableChannel) Start(context.Context) error { return nil }

func (c *reactionCapableChannel) Stop(context.Context) error { return nil }

// TestBaseChannelAutoRegistersReactionUndo pins the inbound orchestration that
// turns ReactionCapable support into an automatic acknowledgement: the undo must
// be handed to the recorder so the Manager can remove the reaction on delivery.
func TestBaseChannelAutoRegistersReactionUndo(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	inner := NewBaseChannel("test", nil, msgBus, nil)
	ch := &reactionCapableChannel{BaseChannel: inner}
	inner.SetOwner(ch)

	recorder := &fakePlaceholderRecorder{}
	inner.SetPlaceholderRecorder(recorder)

	err := inner.HandleMessageWithContext(
		context.Background(),
		"42",
		"hello",
		nil,
		bus.InboundContext{ChatID: "42", MessageID: "99", SenderID: "1"},
	)
	if err != nil {
		t.Fatalf("HandleMessageWithContext: %v", err)
	}

	if ch.reactions != 1 {
		t.Fatalf("ReactToMessage calls = %d, want 1", ch.reactions)
	}
	if ch.lastMsgID != "99" {
		t.Fatalf("reacted to message %q, want 99 (the inbound message, not the reply)", ch.lastMsgID)
	}
	if recorder.reactionUndos != 1 {
		t.Fatalf("RecordReactionUndo calls = %d, want 1", recorder.reactionUndos)
	}
}

// TestBaseChannelSkipsReactionWithoutMessageID guards channels that report no
// message ID: reacting is impossible and would otherwise produce an API error
// on every inbound message.
func TestBaseChannelSkipsReactionWithoutMessageID(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	inner := NewBaseChannel("test", nil, msgBus, nil)
	ch := &reactionCapableChannel{BaseChannel: inner}
	inner.SetOwner(ch)

	recorder := &fakePlaceholderRecorder{}
	inner.SetPlaceholderRecorder(recorder)

	err := inner.HandleMessageWithContext(
		context.Background(),
		"42",
		"hello",
		nil,
		bus.InboundContext{ChatID: "42", SenderID: "1"},
	)
	if err != nil {
		t.Fatalf("HandleMessageWithContext: %v", err)
	}

	if ch.reactions != 0 {
		t.Fatalf("ReactToMessage calls = %d, want 0 without a message ID", ch.reactions)
	}
	if recorder.reactionUndos != 0 {
		t.Fatalf("RecordReactionUndo calls = %d, want 0", recorder.reactionUndos)
	}
}
