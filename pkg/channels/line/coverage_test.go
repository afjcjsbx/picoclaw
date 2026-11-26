package line

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/line/line-bot-sdk-go/v8/linebot/messaging_api"
	"github.com/line/line-bot-sdk-go/v8/linebot/webhook"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
)

func newCoverageLINE(t *testing.T, server *httptest.Server, allowFrom ...string) *LINEChannel {
	t.Helper()
	messageBus := bus.NewMessageBus()
	ch, err := NewLINEChannel(
		&config.Channel{AllowFrom: config.FlexibleStringSlice(allowFrom)},
		&config.LINESettings{
			ChannelSecret:      *config.NewSecureString("secret"),
			ChannelAccessToken: *config.NewSecureString("token"),
		},
		messageBus,
	)
	if err != nil {
		t.Fatalf("NewLINEChannel() error = %v", err)
	}
	if server != nil {
		client, err := messaging_api.NewMessagingApiAPI(
			"token",
			messaging_api.WithEndpoint(server.URL),
			messaging_api.WithHTTPClient(server.Client()),
		)
		if err != nil {
			t.Fatalf("NewMessagingApiAPI() error = %v", err)
		}
		ch.client = client
	}
	ch.ctx = context.Background()
	ch.SetRunning(true)
	t.Cleanup(func() { _ = ch.Stop(context.Background()) })
	return ch
}

func newCoverageLINEBusChannel(t *testing.T, allowFrom ...string) (*LINEChannel, *bus.MessageBus) {
	t.Helper()
	messageBus := bus.NewMessageBus()
	ch, err := NewLINEChannel(
		&config.Channel{AllowFrom: config.FlexibleStringSlice(allowFrom)},
		&config.LINESettings{
			ChannelSecret:      *config.NewSecureString("secret"),
			ChannelAccessToken: *config.NewSecureString("token"),
		},
		messageBus,
	)
	if err != nil {
		t.Fatalf("NewLINEChannel() error = %v", err)
	}
	ch.ctx = context.Background()
	return ch, messageBus
}

type coverageLINERequest struct {
	Method        string
	Path          string
	Authorization string
	Body          json.RawMessage
}

func newCoverageLINEAPIServer(
	t *testing.T,
	status int,
	responseBody string,
) (*httptest.Server, <-chan coverageLINERequest) {
	t.Helper()
	requests := make(chan coverageLINERequest, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- coverageLINERequest{
			Method:        r.Method,
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			Body:          body,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(responseBody))
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func readCoverageLINERequest(t *testing.T, requests <-chan coverageLINERequest) coverageLINERequest {
	t.Helper()
	select {
	case req := <-requests:
		return req
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for LINE API request")
		return coverageLINERequest{}
	}
}

func assertNoCoverageLINERequest(t *testing.T, requests <-chan coverageLINERequest) {
	t.Helper()
	select {
	case req := <-requests:
		t.Fatalf("unexpected LINE API request: %+v", req)
	default:
	}
}

func waitCoverageLINEInbound(t *testing.T, messageBus *bus.MessageBus) bus.InboundMessage {
	t.Helper()
	select {
	case inbound := <-messageBus.InboundChan():
		return inbound
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for LINE inbound message")
		return bus.InboundMessage{}
	}
}

func assertNoCoverageLINEInbound(t *testing.T, messageBus *bus.MessageBus) {
	t.Helper()
	select {
	case inbound := <-messageBus.InboundChan():
		t.Fatalf("unexpected LINE inbound message: %+v", inbound)
	default:
	}
}

func signedCoverageLINERequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhook/line", strings.NewReader(body))
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(body))
	req.Header.Set("X-Line-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return req
}

func TestNewLINEChannelValidationAndDefaults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		secret string
		token  string
	}{
		{name: "missing secret", token: "token"},
		{name: "missing token", secret: "secret"},
		{name: "missing both"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewLINEChannel(&config.Channel{}, &config.LINESettings{
				ChannelSecret:      *config.NewSecureString(tc.secret),
				ChannelAccessToken: *config.NewSecureString(tc.token),
			}, bus.NewMessageBus())
			if err == nil {
				t.Fatal("NewLINEChannel() should reject missing credentials")
			}
		})
	}

	ch, _ := newCoverageLINEBusChannel(t)
	if got := ch.WebhookPath(); got != "/webhook/line" {
		t.Fatalf("WebhookPath() = %q", got)
	}
	ch.config.WebhookPath = "/custom/line"
	if got := ch.WebhookPath(); got != "/custom/line" {
		t.Fatalf("WebhookPath() = %q", got)
	}
	if got := ch.MaxMessageLength(); got != 5000 {
		t.Fatalf("MaxMessageLength() = %d, want 5000", got)
	}
	if got := ch.VoiceCapabilities(); !got.ASR || !got.TTS {
		t.Fatalf("VoiceCapabilities() = %+v", got)
	}
}

func TestLINEStart_FetchesBotInfoAndToleratesAPIError(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server, requests := newCoverageLINEAPIServer(
			t,
			http.StatusOK,
			`{"userId":"Ubot","basicId":"@bot","displayName":"Pico"}`,
		)
		ch := newCoverageLINE(t, server)
		if err := ch.Start(context.Background()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if !ch.IsRunning() || ch.botUserID != "Ubot" || ch.botBasicID != "@bot" || ch.botDisplayName != "Pico" {
			t.Fatalf(
				"channel state after Start = running:%v user:%q basic:%q name:%q",
				ch.IsRunning(),
				ch.botUserID,
				ch.botBasicID,
				ch.botDisplayName,
			)
		}
		req := readCoverageLINERequest(t, requests)
		if req.Method != http.MethodGet || req.Path != "/v2/bot/info" || req.Authorization != "Bearer token" {
			t.Fatalf("GetBotInfo request = %+v", req)
		}
		if err := ch.Stop(context.Background()); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
		if ch.IsRunning() {
			t.Fatal("channel should be stopped")
		}
		select {
		case <-ch.ctx.Done():
		default:
			t.Fatal("Stop should cancel the channel context")
		}
	})

	t.Run("API error", func(t *testing.T) {
		server, _ := newCoverageLINEAPIServer(t, http.StatusInternalServerError, `{"message":"unavailable"}`)
		ch := newCoverageLINE(t, server)
		if err := ch.Start(context.Background()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if !ch.IsRunning() || ch.botUserID != "" || ch.botDisplayName != "" {
			t.Fatalf("channel should run without bot info: %+v", ch)
		}
	})
}

func TestWebhookHandler_SignedMalformedAndEmptyPayloads(t *testing.T) {
	ch, _ := newCoverageLINEBusChannel(t)

	rec := httptest.NewRecorder()
	ch.ServeHTTP(rec, signedCoverageLINERequest(t, `{"events":`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed webhook status = %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	ch.ServeHTTP(rec, signedCoverageLINERequest(t, `{"destination":"Ubot","events":[]}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("empty webhook status = %d, want 200", rec.Code)
	}
}

func TestWebhookHandler_ProcessesSignedTextEvent(t *testing.T) {
	ch, messageBus := newCoverageLINEBusChannel(t)
	body := `{
		"destination":"Ubot",
		"events":[{
			"type":"message",
			"source":{"type":"user","userId":"U123"},
			"timestamp":1,
			"mode":"active",
			"webhookEventId":"event-1",
			"replyToken":"reply-1",
			"message":{"type":"text","id":"M1","text":"hello","quoteToken":"quote-1"}
		}]
	}`
	rec := httptest.NewRecorder()
	ch.ServeHTTP(rec, signedCoverageLINERequest(t, body))
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook status = %d, want 200", rec.Code)
	}

	inbound := waitCoverageLINEInbound(t, messageBus)
	if inbound.Content != "hello" || inbound.Context.ChatID != "U123" || inbound.Context.ChatType != "direct" {
		t.Fatalf("inbound = %+v", inbound)
	}
	if inbound.Context.MessageID != "M1" || inbound.Context.Raw["source_type"] != "user" {
		t.Fatalf("inbound context = %+v", inbound.Context)
	}
	if inbound.Sender.CanonicalID != "line:U123" || inbound.Context.ReplyHandles["reply_token"] != "reply-1" ||
		inbound.Context.ReplyHandles["quote_token"] != "quote-1" {
		t.Fatalf("inbound sender/reply handles = %+v / %v", inbound.Sender, inbound.Context.ReplyHandles)
	}
	if entry, ok := ch.replyTokens.Load("U123"); !ok || entry.(replyTokenEntry).token != "reply-1" {
		t.Fatalf("cached reply token = %v, %v", entry, ok)
	}
	if token, ok := ch.quoteTokens.Load("U123"); !ok || token != "quote-1" {
		t.Fatalf("cached quote token = %v, %v", token, ok)
	}
}

func TestProcessEvent_TextGroupFilteringAndNonMessages(t *testing.T) {
	t.Run("group mention", func(t *testing.T) {
		ch, messageBus := newCoverageLINEBusChannel(t)
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
		inbound := waitCoverageLINEInbound(t, messageBus)
		if inbound.Content != "hello" || !inbound.Context.Mentioned || inbound.Context.ChatID != "C123" ||
			inbound.Context.ChatType != "group" {
			t.Fatalf("group inbound = %+v", inbound)
		}
		if inbound.Context.Raw["source_type"] != "group" {
			t.Fatalf("source metadata = %v", inbound.Context.Raw)
		}
	})

	t.Run("prefix trigger", func(t *testing.T) {
		ch, messageBus := newCoverageLINEBusChannel(t)
		ch.BaseChannel = channels.NewBaseChannel("line", ch.config, messageBus, nil,
			channels.WithGroupTrigger(config.GroupTriggerConfig{Prefixes: []string{"/bot"}}))
		ch.processEvent(webhook.MessageEvent{
			Event:   webhook.Event{Type: "message"},
			Source:  webhook.RoomSource{RoomId: "R123", UserId: "U123"},
			Message: webhook.TextMessageContent{Id: "M2", Text: "/bot hello"},
		})
		inbound := waitCoverageLINEInbound(t, messageBus)
		if inbound.Content != "hello" || inbound.Context.ChatType != "group" ||
			inbound.Context.Raw["source_type"] != "room" {
			t.Fatalf("room inbound = %+v", inbound)
		}
	})

	t.Run("ignored events", func(t *testing.T) {
		ch, messageBus := newCoverageLINEBusChannel(t, "line:other")
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
		assertNoCoverageLINEInbound(t, messageBus)
	})
}

func TestProcessEvent_NonDownloadedMessageTypes(t *testing.T) {
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
			ch, messageBus := newCoverageLINEBusChannel(t)
			ch.processEvent(webhook.MessageEvent{
				Event:          webhook.Event{Type: "message"},
				Source:         webhook.UserSource{UserId: "U123"},
				ReplyToken:     "reply-1",
				Message:        tc.message,
				WebhookEventId: "event-1",
			})
			inbound := waitCoverageLINEInbound(t, messageBus)
			if inbound.Content != tc.want || inbound.Context.MessageID == "" {
				t.Fatalf("inbound = %+v, want content %q", inbound, tc.want)
			}
		})
	}
}

func TestResolveSource(t *testing.T) {
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
		{name: "room", source: webhook.RoomSource{UserId: "U1", RoomId: "R1"}, senderID: "U1", chatID: "R1", sourceType: "room"},
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

func TestIsBotMentioned(t *testing.T) {
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

func TestStripBotMention(t *testing.T) {
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

func TestSend_ReplyPushAndQuoteToken(t *testing.T) {
	t.Run("reply", func(t *testing.T) {
		server, requests := newCoverageLINEAPIServer(t, http.StatusOK, `{}`)
		ch := newCoverageLINE(t, server)
		ch.replyTokens.Store("U123", replyTokenEntry{token: "reply-1", timestamp: time.Now()})
		ch.quoteTokens.Store("U123", "quote-1")
		if _, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "U123", Content: "hello"}); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		req := readCoverageLINERequest(t, requests)
		if req.Path != "/v2/bot/message/reply" || req.Authorization != "Bearer token" {
			t.Fatalf("reply request = %+v", req)
		}
		var body struct {
			ReplyToken string `json:"replyToken"`
			Messages   []struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				QuoteToken string `json:"quoteToken"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(req.Body, &body); err != nil {
			t.Fatalf("json.Unmarshal(request) error = %v", err)
		}
		if body.ReplyToken != "reply-1" || len(body.Messages) != 1 || body.Messages[0].Text != "hello" ||
			body.Messages[0].QuoteToken != "quote-1" {
			t.Fatalf("reply body = %+v", body)
		}
		if _, ok := ch.replyTokens.Load("U123"); ok {
			t.Fatal("reply token should be consumed")
		}
		if _, ok := ch.quoteTokens.Load("U123"); ok {
			t.Fatal("quote token should be consumed")
		}
		assertNoCoverageLINERequest(t, requests)
	})

	t.Run("expired reply token uses push", func(t *testing.T) {
		server, requests := newCoverageLINEAPIServer(t, http.StatusOK, `{}`)
		ch := newCoverageLINE(t, server)
		ch.replyTokens.Store(
			"U123",
			replyTokenEntry{token: "old", timestamp: time.Now().Add(-lineReplyTokenMaxAge - time.Second)},
		)
		if _, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "U123", Content: "hello"}); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		req := readCoverageLINERequest(t, requests)
		if req.Path != "/v2/bot/message/push" || !strings.Contains(string(req.Body), `"to":"U123"`) {
			t.Fatalf("push request = %+v", req)
		}
	})

	t.Run("reply error falls back to push", func(t *testing.T) {
		requests := make(chan coverageLINERequest, 4)
		var replyCount atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			requests <- coverageLINERequest{Method: r.Method, Path: r.URL.Path, Body: body}
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/v2/bot/message/reply" {
				replyCount.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"reply failed"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}))
		defer server.Close()
		ch := newCoverageLINE(t, server)
		ch.replyTokens.Store("U123", replyTokenEntry{token: "reply-1", timestamp: time.Now()})
		ch.quoteTokens.Store("U123", "quote-1")
		if _, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "U123", Content: "hello"}); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if got := readCoverageLINERequest(t, requests).Path; got != "/v2/bot/message/reply" {
			t.Fatalf("first path = %q", got)
		}
		push := readCoverageLINERequest(t, requests)
		if push.Path != "/v2/bot/message/push" || replyCount.Load() != 1 ||
			!strings.Contains(string(push.Body), `"quoteToken":"quote-1"`) {
			t.Fatalf("fallback push = %+v, reply count %d", push, replyCount.Load())
		}
	})
}

func TestSend_PushErrorsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   error
	}{
		{name: "bad request", status: http.StatusBadRequest, want: channels.ErrSendFailed},
		{name: "rate limited", status: http.StatusTooManyRequests, want: channels.ErrRateLimit},
		{name: "server error", status: http.StatusInternalServerError, want: channels.ErrTemporary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := newCoverageLINEAPIServer(t, tc.status, `{"message":"failed"}`)
			ch := newCoverageLINE(t, server)
			_, err := ch.Send(context.Background(), bus.OutboundMessage{
				ChatID: "U123", Content: "hello",
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Send() error = %v, want %v", err, tc.want)
			}
			if got := readCoverageLINERequest(t, requests).Path; got != "/v2/bot/message/push" {
				t.Fatalf("request path = %q", got)
			}
		})
	}

	t.Run("not running", func(t *testing.T) {
		ch, _ := newCoverageLINEBusChannel(t)
		if _, err := ch.Send(context.Background(), bus.OutboundMessage{}); !errors.Is(err, channels.ErrNotRunning) {
			t.Fatalf("Send() error = %v, want ErrNotRunning", err)
		}
		if _, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{}); !errors.Is(
			err,
			channels.ErrNotRunning,
		) {
			t.Fatalf("SendMedia() error = %v, want ErrNotRunning", err)
		}
	})

	t.Run("network error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		ch := newCoverageLINE(t, server)
		server.Close()
		if _, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "U123", Content: "hello"}); !errors.Is(
			err,
			channels.ErrTemporary,
		) {
			t.Fatalf("Send() network error = %v, want ErrTemporary", err)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		server, requests := newCoverageLINEAPIServer(t, http.StatusOK, `{}`)
		ch := newCoverageLINE(t, server)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := ch.Send(ctx, bus.OutboundMessage{ChatID: "U123", Content: "hello"}); !errors.Is(
			err,
			context.Canceled,
		) {
			t.Fatalf("Send() canceled error = %v", err)
		}
		assertNoCoverageLINERequest(t, requests)
	})
}

func TestSendMedia_CaptionFallbackAndErrors(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server, requests := newCoverageLINEAPIServer(t, http.StatusOK, `{}`)
		ch := newCoverageLINE(t, server)
		ch.SetMediaStore(media.NewFileMediaStore())
		msg := bus.OutboundMediaMessage{ChatID: "U123", Parts: []bus.MediaPart{
			{Type: "image", Filename: "picture.png", Caption: "picture caption"},
			{Type: "video", Filename: "clip.mp4"},
		}}
		if _, err := ch.SendMedia(context.Background(), msg); err != nil {
			t.Fatalf("SendMedia() error = %v", err)
		}
		for i, want := range []string{"picture caption", "[video: clip.mp4]"} {
			req := readCoverageLINERequest(t, requests)
			if req.Path != "/v2/bot/message/push" || !strings.Contains(string(req.Body), `"text":"`+want+`"`) {
				t.Fatalf("request %d = %+v, want text %q", i, req, want)
			}
		}
		assertNoCoverageLINERequest(t, requests)
	})

	t.Run("missing store", func(t *testing.T) {
		ch, _ := newCoverageLINEBusChannel(t)
		ch.SetRunning(true)
		if _, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{}); !errors.Is(
			err,
			channels.ErrSendFailed,
		) {
			t.Fatalf("SendMedia() error = %v, want ErrSendFailed", err)
		}
	})

	t.Run("API error stops remaining parts", func(t *testing.T) {
		server, requests := newCoverageLINEAPIServer(t, http.StatusBadRequest, `{"message":"failed"}`)
		ch := newCoverageLINE(t, server)
		ch.SetMediaStore(media.NewFileMediaStore())
		_, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{Parts: []bus.MediaPart{
			{Type: "image", Caption: "first"},
			{Type: "video", Caption: "second"},
		}})
		if !errors.Is(err, channels.ErrSendFailed) {
			t.Fatalf("SendMedia() error = %v, want ErrSendFailed", err)
		}
		_ = readCoverageLINERequest(t, requests)
		assertNoCoverageLINERequest(t, requests)
	})
}

func TestStartTyping_LocalAPIAndNoOpChats(t *testing.T) {
	t.Run("no-op", func(t *testing.T) {
		server, requests := newCoverageLINEAPIServer(t, http.StatusOK, `{}`)
		ch := newCoverageLINE(t, server)
		for _, chatID := range []string{"", "C123", "R123"} {
			stop, err := ch.StartTyping(context.Background(), chatID)
			if err != nil || stop == nil {
				t.Fatalf("StartTyping(%q) returned nil stop=%v, err=%v", chatID, stop == nil, err)
			}
			stop()
		}
		assertNoCoverageLINERequest(t, requests)
	})

	t.Run("user chat", func(t *testing.T) {
		server, requests := newCoverageLINEAPIServer(t, http.StatusOK, `{}`)
		ch := newCoverageLINE(t, server)
		stop, err := ch.StartTyping(context.Background(), "U123")
		if err != nil {
			t.Fatalf("StartTyping() error = %v", err)
		}
		req := readCoverageLINERequest(t, requests)
		if req.Path != "/v2/bot/chat/loading/start" || !strings.Contains(string(req.Body), `"chatId":"U123"`) ||
			!strings.Contains(string(req.Body), `"loadingSeconds":60`) {
			t.Fatalf("loading request = %+v", req)
		}
		stop()
		stop()
	})

	t.Run("API error", func(t *testing.T) {
		server, _ := newCoverageLINEAPIServer(t, http.StatusInternalServerError, `{"message":"failed"}`)
		ch := newCoverageLINE(t, server)
		stop, err := ch.StartTyping(context.Background(), "U123")
		if !errors.Is(err, channels.ErrTemporary) || stop == nil {
			t.Fatalf("StartTyping() returned nil stop=%v, err=%v", stop == nil, err)
		}
		stop()
	})
}

func TestClassifySDKError(t *testing.T) {
	if err := classifySDKError(nil, nil); err != nil {
		t.Fatalf("classifySDKError(nil, nil) = %v", err)
	}
	if err := classifySDKError(nil, errors.New("network")); !errors.Is(err, channels.ErrTemporary) {
		t.Fatalf("network error = %v, want ErrTemporary", err)
	}
	for _, tc := range []struct {
		status int
		want   error
	}{
		{status: http.StatusBadRequest, want: channels.ErrSendFailed},
		{status: http.StatusTooManyRequests, want: channels.ErrRateLimit},
		{status: http.StatusServiceUnavailable, want: channels.ErrTemporary},
	} {
		resp := &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader("body"))}
		if err := classifySDKError(resp, errors.New("api")); !errors.Is(err, tc.want) {
			t.Errorf("classifySDKError(%d) = %v, want %v", tc.status, err, tc.want)
		}
	}
}
