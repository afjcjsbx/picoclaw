package line

import (
	"testing"

	"github.com/line/line-bot-sdk-go/v8/linebot/webhook"

	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestLINEProcessEventTextGroupFilteringAndNonMessages(t *testing.T) {
	t.Run("group mention", func(t *testing.T) {
		ch, messageBus := newLINETestBusChannel(t)
		ch.BaseChannel = channels.NewBaseChannel("line", ch.config, messageBus, nil,
			channels.WithGroupTrigger(config.GroupTriggerConfig{MentionOnly: true}))
		ch.botUserID = "Ubot"
		event := webhook.MessageEvent{
			Event:          webhook.Event{Type: "message"},
			Source:         webhook.GroupSource{GroupId: "C123", UserId: "U123"},
			ReplyToken:     "reply-1",
			Message:        webhook.TextMessageContent{Id: "M1", Text: "@Pico hello"},
			WebhookEventId: "event-1",
		}
		event.Message = webhook.TextMessageContent{
			Id:         "M1",
			Text:       "@Pico hello",
			QuoteToken: "quote-1",
			Mention: &webhook.Mention{Mentionees: []webhook.MentioneeInterface{
				webhook.UserMentionee{Index: 0, Length: 5, IsSelf: true},
			}},
		}
		ch.botDisplayName = "Pico"
		ch.processEvent(event)
		inbound := waitLINETestInbound(t, messageBus)
		if inbound.Content != "hello" || !inbound.Context.Mentioned || inbound.Context.ChatID != "C123" ||
			inbound.Context.ChatType != "group" {
			t.Fatalf("group inbound = %+v", inbound)
		}
		if inbound.Context.Raw["source_type"] != "group" {
			t.Fatalf("source metadata = %v", inbound.Context.Raw)
		}
	})

	t.Run("prefix trigger", func(t *testing.T) {
		ch, messageBus := newLINETestBusChannel(t)
		ch.BaseChannel = channels.NewBaseChannel("line", ch.config, messageBus, nil,
			channels.WithGroupTrigger(config.GroupTriggerConfig{Prefixes: []string{"/bot"}}))
		ch.processEvent(webhook.MessageEvent{
			Event:   webhook.Event{Type: "message"},
			Source:  webhook.RoomSource{RoomId: "R123", UserId: "U123"},
			Message: webhook.TextMessageContent{Id: "M2", Text: "/bot hello"},
		})
		inbound := waitLINETestInbound(t, messageBus)
		if inbound.Content != "hello" || inbound.Context.ChatType != "group" ||
			inbound.Context.Raw["source_type"] != "room" {
			t.Fatalf("room inbound = %+v", inbound)
		}
	})

	t.Run("ignored events", func(t *testing.T) {
		ch, messageBus := newLINETestBusChannel(t, "line:other")
		ch.BaseChannel = channels.NewBaseChannel("line", ch.config, messageBus, []string{"line:other"},
			channels.WithGroupTrigger(config.GroupTriggerConfig{MentionOnly: true}))
		for _, event := range []webhook.EventInterface{
			webhook.Event{Type: "follow"},
			webhook.MessageEvent{Source: webhook.UserSource{UserId: "U123"}, Message: webhook.UnknownMessageContent{Type: "unknown"}},
			webhook.MessageEvent{Source: webhook.UserSource{UserId: "U123"}, Message: webhook.TextMessageContent{Id: "M3", Text: " "}},
			webhook.MessageEvent{Source: webhook.GroupSource{GroupId: "C123", UserId: "U123"}, Message: webhook.TextMessageContent{Id: "M4", Text: "no mention"}},
			webhook.MessageEvent{Source: webhook.UserSource{UserId: "U123"}, Message: webhook.TextMessageContent{Id: "M5", Text: "not allowed"}},
		} {
			ch.processEvent(event)
		}
		assertNoLINETestInbound(t, messageBus)
	})
}

func TestLINEProcessEventNonDownloadedMessageTypes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message webhook.MessageContentInterface
		want    string
	}{
		{name: "file", message: webhook.FileMessageContent{Id: "M1", FileName: "document.pdf"}, want: "[file]"},
		{name: "location", message: webhook.LocationMessageContent{Id: "M2", Title: "Office"}, want: "[location: Office]"},
		{name: "location without title", message: webhook.LocationMessageContent{Id: "M3"}, want: "[location]"},
		{name: "sticker", message: webhook.StickerMessageContent{Id: "M4"}, want: "[sticker]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch, messageBus := newLINETestBusChannel(t)
			ch.processEvent(webhook.MessageEvent{
				Event:          webhook.Event{Type: "message"},
				Source:         webhook.UserSource{UserId: "U123"},
				ReplyToken:     "reply-1",
				Message:        tc.message,
				WebhookEventId: "event-1",
			})
			inbound := waitLINETestInbound(t, messageBus)
			if inbound.Content != tc.want || inbound.Context.MessageID == "" {
				t.Fatalf("inbound = %+v, want content %q", inbound, tc.want)
			}
		})
	}
}

func TestLINEResolveSource(t *testing.T) {
	ch := &LINEChannel{}
	for _, tc := range []struct {
		name       string
		source     webhook.SourceInterface
		senderID   string
		chatID     string
		sourceType string
	}{
		{name: "user", source: webhook.UserSource{UserId: "U1"}, senderID: "U1", chatID: "U1", sourceType: "user"},
		{name: "group", source: webhook.GroupSource{UserId: "U1", GroupId: "C1"}, senderID: "U1", chatID: "C1", sourceType: "group"},
		{name: "room", source: webhook.RoomSource{RoomId: "R1", UserId: "U1"}, senderID: "U1", chatID: "R1", sourceType: "room"},
		{name: "unknown", source: nil, sourceType: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			senderID, chatID, sourceType := ch.resolveSource(tc.source)
			if senderID != tc.senderID || chatID != tc.chatID || sourceType != tc.sourceType {
				t.Fatalf("resolveSource() = %q, %q, %q", senderID, chatID, sourceType)
			}
		})
	}
}

func TestLINEIsBotMentioned(t *testing.T) {
	ch := &LINEChannel{botUserID: "Ubot", botDisplayName: "Pico"}
	for _, tc := range []struct {
		name string
		msg  webhook.TextMessageContent
		want bool
	}{
		{name: "none", msg: webhook.TextMessageContent{Text: "hello"}},
		{name: "all", msg: webhook.TextMessageContent{Mention: &webhook.Mention{Mentionees: []webhook.MentioneeInterface{webhook.AllMentionee{}}}}, want: true},
		{name: "self", msg: webhook.TextMessageContent{Mention: &webhook.Mention{Mentionees: []webhook.MentioneeInterface{webhook.UserMentionee{IsSelf: true}}}}, want: true},
		{name: "user ID", msg: webhook.TextMessageContent{Mention: &webhook.Mention{Mentionees: []webhook.MentioneeInterface{webhook.UserMentionee{UserId: "Ubot"}}}}, want: true},
		{name: "display name by index", msg: webhook.TextMessageContent{Text: "@Pico hi", Mention: &webhook.Mention{Mentionees: []webhook.MentioneeInterface{webhook.UserMentionee{UserId: "other", Index: 0, Length: 5}}}}, want: true},
		{name: "unicode display name by index", msg: webhook.TextMessageContent{Text: "你好 @Pico hi", Mention: &webhook.Mention{Mentionees: []webhook.MentioneeInterface{webhook.UserMentionee{UserId: "other", Index: 3, Length: 5}}}}, want: true},
		{name: "out of range", msg: webhook.TextMessageContent{Text: "hello", Mention: &webhook.Mention{Mentionees: []webhook.MentioneeInterface{webhook.UserMentionee{UserId: "other", Index: 20, Length: 5}}}}},
		{name: "fallback", msg: webhook.TextMessageContent{Text: "hello @Pico"}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ch.isBotMentioned(tc.msg); got != tc.want {
				t.Fatalf("isBotMentioned() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLINEStripBotMention(t *testing.T) {
	ch := &LINEChannel{botUserID: "Ubot", botDisplayName: "Pico"}
	for _, tc := range []struct {
		name string
		text string
		msg  webhook.TextMessageContent
		want string
	}{
		{name: "fallback", text: "hello @Pico", want: "hello"},
		{name: "all mention preserved", text: "@All hello", msg: webhook.TextMessageContent{Mention: &webhook.Mention{Mentionees: []webhook.MentioneeInterface{webhook.AllMentionee{Index: 0, Length: 4}}}}, want: "@All hello"},
		{name: "self", text: "@Pico hello", msg: webhook.TextMessageContent{Mention: &webhook.Mention{Mentionees: []webhook.MentioneeInterface{webhook.UserMentionee{Index: 0, Length: 5, IsSelf: true}}}}, want: "hello"},
		{name: "bot ID", text: "@Pico hello", msg: webhook.TextMessageContent{Mention: &webhook.Mention{Mentionees: []webhook.MentioneeInterface{webhook.UserMentionee{Index: 0, Length: 5, UserId: "Ubot"}}}}, want: "hello"},
		{name: "display name", text: "@Pico hello", msg: webhook.TextMessageContent{Mention: &webhook.Mention{Mentionees: []webhook.MentioneeInterface{webhook.UserMentionee{Index: 0, Length: 5, UserId: "other"}}}}, want: "hello"},
		{name: "multiple reverse order", text: "@Pico @Pico hello", msg: webhook.TextMessageContent{Mention: &webhook.Mention{Mentionees: []webhook.MentioneeInterface{
			webhook.UserMentionee{Index: 0, Length: 5, IsSelf: true},
			webhook.UserMentionee{Index: 6, Length: 5, IsSelf: true},
		}}}, want: "hello"},
		{name: "invalid index", text: "hello", msg: webhook.TextMessageContent{Mention: &webhook.Mention{Mentionees: []webhook.MentioneeInterface{webhook.UserMentionee{Index: -1, Length: 5, IsSelf: true}}}}, want: "hello"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ch.stripBotMention(tc.text, tc.msg); got != tc.want {
				t.Fatalf("stripBotMention() = %q, want %q", got, tc.want)
			}
		})
	}
}
