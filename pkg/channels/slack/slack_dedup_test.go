package slack

import (
	"context"
	"testing"
	"time"

	"github.com/slack-go/slack/slackevents"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

func newSlackDedupTestChannel(t *testing.T, groupTrigger config.GroupTriggerConfig) (*SlackChannel, *bus.MessageBus) {
	t.Helper()

	msgBus := bus.NewMessageBus()
	bc := &config.Channel{
		Type:         config.ChannelSlack,
		Enabled:      true,
		AllowFrom:    []string{"*"},
		GroupTrigger: groupTrigger,
	}
	cfg := &config.SlackSettings{}
	cfg.BotToken = *config.NewSecureString("xoxb-test")
	cfg.AppToken = *config.NewSecureString("xapp-test")

	ch, err := NewSlackChannel(bc, cfg, msgBus)
	if err != nil {
		t.Fatalf("NewSlackChannel() error = %v", err)
	}
	ch.ctx = context.Background()
	ch.botUserID = "UBOT"
	ch.teamID = "T1"
	return ch, msgBus
}

func nextSlackInbound(t *testing.T, msgBus *bus.MessageBus) bus.InboundMessage {
	t.Helper()

	select {
	case msg := <-msgBus.InboundChan():
		return msg
	case <-time.After(time.Second):
		t.Fatal("expected inbound message")
		return bus.InboundMessage{}
	}
}

func assertNoSlackInbound(t *testing.T, msgBus *bus.MessageBus) {
	t.Helper()

	select {
	case msg := <-msgBus.InboundChan():
		t.Fatalf("unexpected duplicate inbound message: %+v", msg)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestSlackMessageAndAppMentionAreDeduplicated(t *testing.T) {
	const (
		channelID = "C123"
		messageTS = "1710000000.000100"
	)

	tests := []struct {
		name  string
		first func(*SlackChannel)
		next  func(*SlackChannel)
	}{
		{
			name: "message first",
			first: func(ch *SlackChannel) {
				ch.handleMessageEvent(&slackevents.MessageEvent{
					User:      "U123",
					Channel:   channelID,
					TimeStamp: messageTS,
					Text:      "hello",
				})
			},
			next: func(ch *SlackChannel) {
				ch.handleAppMention(&slackevents.AppMentionEvent{
					User:      "U123",
					Channel:   channelID,
					TimeStamp: messageTS,
					Text:      "<@UBOT> hello",
				})
			},
		},
		{
			name: "app mention first",
			first: func(ch *SlackChannel) {
				ch.handleAppMention(&slackevents.AppMentionEvent{
					User:      "U123",
					Channel:   channelID,
					TimeStamp: messageTS,
					Text:      "<@UBOT> hello",
				})
			},
			next: func(ch *SlackChannel) {
				ch.handleMessageEvent(&slackevents.MessageEvent{
					User:      "U123",
					Channel:   channelID,
					TimeStamp: messageTS,
					Text:      "hello",
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, msgBus := newSlackDedupTestChannel(t, config.GroupTriggerConfig{})

			tt.first(ch)
			first := nextSlackInbound(t, msgBus)
			if first.Context.ChatID != channelID {
				t.Fatalf("ChatID = %q, want %q", first.Context.ChatID, channelID)
			}
			if first.MediaScope != "slack:"+channelID+":"+messageTS {
				t.Fatalf("MediaScope = %q, want slack:%s:%s", first.MediaScope, channelID, messageTS)
			}

			tt.next(ch)
			assertNoSlackInbound(t, msgBus)
		})
	}
}

func TestSlackDistinctMessagesAreNotDeduplicated(t *testing.T) {
	ch, msgBus := newSlackDedupTestChannel(t, config.GroupTriggerConfig{})

	ch.handleMessageEvent(&slackevents.MessageEvent{
		User:      "U123",
		Channel:   "C123",
		TimeStamp: "1710000000.000100",
		Text:      "first",
	})
	ch.handleMessageEvent(&slackevents.MessageEvent{
		User:      "U123",
		Channel:   "C123",
		TimeStamp: "1710000000.000200",
		Text:      "second",
	})

	first := nextSlackInbound(t, msgBus)
	second := nextSlackInbound(t, msgBus)
	if first.Content != "first" || second.Content != "second" {
		t.Fatalf("inbound contents = %q, %q, want first, second", first.Content, second.Content)
	}
}

func TestSlackMessageEventDetectsBotMention(t *testing.T) {
	ch, msgBus := newSlackDedupTestChannel(t, config.GroupTriggerConfig{MentionOnly: true})

	ch.handleMessageEvent(&slackevents.MessageEvent{
		User:      "U123",
		Channel:   "C123",
		TimeStamp: "1710000000.000300",
		Text:      "<@UBOT> hello",
	})

	msg := nextSlackInbound(t, msgBus)
	if msg.Content != "hello" {
		t.Fatalf("Content = %q, want hello", msg.Content)
	}
}
