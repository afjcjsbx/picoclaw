package vk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SevereCloud/vksdk/v3/api"
	"github.com/SevereCloud/vksdk/v3/object"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
)

func newVKHTTPTestChannel(
	t *testing.T,
	bc *config.Channel,
	handler http.HandlerFunc,
) (*VKChannel, *bus.MessageBus) {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	messageBus := bus.NewMessageBus()
	ch, err := NewVKChannel("vk", bc, messageBus)
	if err != nil {
		t.Fatalf("NewVKChannel() error: %v", err)
	}
	vk := api.NewVK("test-token")
	vk.MethodURL = server.URL + "/method/"
	vk.Client = server.Client()
	vk.Limit = 1000
	ch.vk = vk
	ch.ctx = context.Background()
	return ch, messageBus
}

func writeVKResponse(t *testing.T, w http.ResponseWriter, response any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		t.Errorf("encode VK response: %v", err)
	}
}

func receiveVKInbound(t *testing.T, messageBus *bus.MessageBus) bus.InboundMessage {
	t.Helper()
	select {
	case inbound := <-messageBus.InboundChan():
		return inbound
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for inbound VK message")
		return bus.InboundMessage{}
	}
}

func assertNoVKInbound(t *testing.T, messageBus *bus.MessageBus) {
	t.Helper()
	select {
	case inbound := <-messageBus.InboundChan():
		t.Fatalf("unexpected inbound VK message: %+v", inbound)
	default:
	}
}

func TestNewVKChannel_InvalidSettings(t *testing.T) {
	_, err := NewVKChannel("vk", &config.Channel{Settings: config.RawNode(`{`)}, bus.NewMessageBus())
	if err == nil {
		t.Fatal("NewVKChannel() expected invalid settings to fail")
	}
}

func TestVKChannel_GetVKCfgInvalidSettings(t *testing.T) {
	ch := &VKChannel{bc: &config.Channel{Settings: config.RawNode(`{`)}}
	if got := ch.getVKCfg(); got != nil {
		t.Fatalf("getVKCfg() = %+v, want nil", got)
	}
}

func TestVKChannel_GetUserName(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{
			name:     "full name",
			response: `{"response":[{"first_name":"Ada","last_name":"Lovelace"}]}`,
			want:     "Ada Lovelace",
		},
		{
			name:     "empty response falls back to id",
			response: `{"response":[]}`,
			want:     "42",
		},
		{
			name:     "api error falls back to id",
			response: `{"error":{"error_code":5,"error_msg":"invalid token"}}`,
			want:     "42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, _ := newVKHTTPTestChannel(t, makeVKTestBaseChannel(config.VKSettings{
				Token:   *config.NewSecureString("test-token"),
				GroupID: 1,
			}), func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/method/users.get" {
					t.Errorf("request path = %q, want /method/users.get", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.response)
			})

			if got := ch.getUserName(42); got != tt.want {
				t.Fatalf("getUserName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVKChannel_SendValidation(t *testing.T) {
	ch := &VKChannel{
		BaseChannel: channels.NewBaseChannel("vk", nil, bus.NewMessageBus(), nil),
	}

	_, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "42", Content: "hello"})
	if !errors.Is(err, channels.ErrNotRunning) {
		t.Fatalf("Send() while stopped error = %v, want ErrNotRunning", err)
	}

	ch.SetRunning(true)
	_, err = ch.Send(context.Background(), bus.OutboundMessage{ChatID: "not-a-peer", Content: "hello"})
	if !errors.Is(err, channels.ErrSendFailed) {
		t.Fatalf("Send() invalid chat error = %v, want ErrSendFailed", err)
	}

	ids, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "42"})
	if err != nil || len(ids) != 0 {
		t.Fatalf("Send() empty content = (%v, %v), want no ids and no error", ids, err)
	}
}

func TestVKChannel_SendSplitsAndReplies(t *testing.T) {
	requests := make(chan url.Values, 4)
	var responseID atomic.Int32
	responseID.Store(100)

	ch, _ := newVKHTTPTestChannel(t, makeVKTestBaseChannel(config.VKSettings{
		Token:   *config.NewSecureString("test-token"),
		GroupID: 1,
	}), func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/method/messages.send" {
			t.Errorf("request path = %q, want /method/messages.send", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm(): %v", err)
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		requests <- r.PostForm
		writeVKResponse(t, w, map[string]any{"response": responseID.Add(1)})
	})
	ch.SetRunning(true)

	ids, err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:           "42",
		Content:          strings.Repeat("a", 4001),
		ReplyToMessageID: "77",
	})
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	if got, want := ids, []string{"101", "102"}; !slices.Equal(got, want) {
		t.Fatalf("Send() ids = %v, want %v", got, want)
	}

	var rebuilt strings.Builder
	for i := 0; i < 2; i++ {
		select {
		case form := <-requests:
			if got := form.Get("peer_id"); got != "42" {
				t.Errorf("request %d peer_id = %q, want 42", i, got)
			}
			chunk := form.Get("message")
			if chunk == "" || len(chunk) > 4000 {
				t.Errorf("request %d message length = %d, want 1..4000", i, len(chunk))
			}
			rebuilt.WriteString(chunk)
			if got := form.Get("reply_to"); got != "77" {
				t.Errorf("request %d reply_to = %q, want 77", i, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for send request %d", i)
		}
	}
	if got := rebuilt.String(); got != strings.Repeat("a", 4001) {
		t.Fatal("sent chunks did not preserve the original message")
	}
}

func TestVKChannel_SendIgnoresInvalidReplyAndReturnsAPIError(t *testing.T) {
	requests := make(chan url.Values, 1)
	ch, _ := newVKHTTPTestChannel(t, makeVKTestBaseChannel(config.VKSettings{
		Token:   *config.NewSecureString("test-token"),
		GroupID: 1,
	}), func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm(): %v", err)
		}
		requests <- r.PostForm
		writeVKResponse(t, w, map[string]any{
			"error": map[string]any{"error_code": 5, "error_msg": "send failed"},
		})
	})
	ch.SetRunning(true)

	ids, err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:           "42",
		Content:          "hello",
		ReplyToMessageID: "not-an-id",
	})
	if err == nil {
		t.Fatal("Send() expected API error")
	}
	if len(ids) != 0 {
		t.Fatalf("Send() ids = %v, want none on first-request failure", ids)
	}

	select {
	case form := <-requests:
		if got := form.Get("reply_to"); got != "" {
			t.Fatalf("reply_to = %q, want invalid reply to be omitted", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for send request")
	}
}

func TestVKChannel_SendReturnsIDsFromSuccessfulChunksBeforeFailure(t *testing.T) {
	var calls atomic.Int32
	ch, _ := newVKHTTPTestChannel(t, makeVKTestBaseChannel(config.VKSettings{
		Token:   *config.NewSecureString("test-token"),
		GroupID: 1,
	}), func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			writeVKResponse(t, w, map[string]any{"response": 101})
			return
		}
		writeVKResponse(t, w, map[string]any{
			"error": map[string]any{"error_code": 5, "error_msg": "send failed"},
		})
	})
	ch.SetRunning(true)

	ids, err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "42",
		Content: strings.Repeat("a", 4001),
	})
	if err == nil {
		t.Fatal("Send() expected second chunk to fail")
	}
	if got, want := ids, []string{"101"}; !slices.Equal(got, want) {
		t.Fatalf("Send() ids = %v, want successful prefix %v", got, want)
	}
}

func TestVKChannel_HandleMessageDirect(t *testing.T) {
	ch, messageBus := newVKHTTPTestChannel(t, makeVKTestBaseChannel(config.VKSettings{
		Token:   *config.NewSecureString("test-token"),
		GroupID: 1,
	}), func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/method/users.get" {
			t.Errorf("request path = %q, want /method/users.get", r.URL.Path)
		}
		writeVKResponse(t, w, map[string]any{
			"response": []map[string]string{{"first_name": "Ada", "last_name": "Lovelace"}},
		})
	})

	ch.handleMessage(object.MessagesMessage{
		FromID:                42,
		PeerID:                42,
		Text:                  "hello",
		ConversationMessageID: 7,
	})

	inbound := receiveVKInbound(t, messageBus)
	if inbound.Content != "hello" || inbound.Context.ChatID != "42" || inbound.Context.ChatType != "direct" {
		t.Fatalf("unexpected inbound message: %+v", inbound)
	}
	if inbound.Context.SenderID != "42" || inbound.Context.MessageID != "7" {
		t.Fatalf("unexpected inbound context: %+v", inbound.Context)
	}
	if inbound.Sender.DisplayName != "Ada Lovelace" || inbound.Sender.CanonicalID != "vk:42" {
		t.Fatalf("unexpected sender: %+v", inbound.Sender)
	}
	if inbound.Context.Raw["is_group"] != "false" {
		t.Fatalf("is_group metadata = %q, want false", inbound.Context.Raw["is_group"])
	}
}

func TestVKChannel_HandleMessageGroupPrefix(t *testing.T) {
	bc := makeVKTestBaseChannel(config.VKSettings{
		Token:   *config.NewSecureString("test-token"),
		GroupID: 1,
	})
	bc.GroupTrigger = config.GroupTriggerConfig{Prefixes: []string{"/bot"}}
	ch, messageBus := newVKHTTPTestChannel(t, bc, func(w http.ResponseWriter, _ *http.Request) {
		writeVKResponse(t, w, map[string]any{
			"response": []map[string]string{{"first_name": "Ada", "last_name": "Lovelace"}},
		})
	})

	ch.handleMessage(object.MessagesMessage{
		FromID:                42,
		PeerID:                2000000001,
		Text:                  "/bot hello",
		ConversationMessageID: 8,
	})

	inbound := receiveVKInbound(t, messageBus)
	if inbound.Content != "hello" || inbound.Context.ChatType != "group" {
		t.Fatalf("unexpected group inbound message: %+v", inbound)
	}
	if inbound.Context.ChatID != "2000000001" || inbound.Context.Raw["is_group"] != "true" {
		t.Fatalf("unexpected group context: %+v", inbound.Context)
	}
}

func TestVKChannel_HandleMessageAttachmentOnly(t *testing.T) {
	ch, messageBus := newVKHTTPTestChannel(t, makeVKTestBaseChannel(config.VKSettings{
		Token:   *config.NewSecureString("test-token"),
		GroupID: 1,
	}), func(w http.ResponseWriter, _ *http.Request) {
		writeVKResponse(t, w, map[string]any{
			"response": []map[string]string{{"first_name": "Ada", "last_name": "Lovelace"}},
		})
	})

	ch.handleMessage(object.MessagesMessage{
		FromID: 42,
		PeerID: 42,
		Attachments: []object.MessagesMessageAttachment{
			{Type: "photo"},
			{Type: "doc", Doc: object.DocsDoc{Title: "report.pdf"}},
		},
	})

	inbound := receiveVKInbound(t, messageBus)
	if got, want := inbound.Content, "[photo] [document: report.pdf]"; got != want {
		t.Fatalf("attachment content = %q, want %q", got, want)
	}
	if len(inbound.Media) != 0 {
		t.Fatalf("media = %v, want attachment annotations only", inbound.Media)
	}
}

func TestVKChannel_HandleMessageIgnoresUnwantedMessages(t *testing.T) {
	tests := []struct {
		name    string
		message object.MessagesMessage
		allow   []string
	}{
		{
			name: "service action",
			message: object.MessagesMessage{
				FromID: 42,
				PeerID: 42,
				Text:   "joined",
				Action: object.MessagesMessageAction{Type: "chat_invite_user"},
			},
		},
		{
			name:    "outgoing",
			message: object.MessagesMessage{FromID: 42, PeerID: 42, Text: "hello", Out: true},
		},
		{
			name:    "empty",
			message: object.MessagesMessage{FromID: 42, PeerID: 42},
		},
		{
			name:    "unauthorized",
			message: object.MessagesMessage{FromID: 42, PeerID: 42, Text: "hello"},
			allow:   []string{"7"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bc := makeVKTestBaseChannel(config.VKSettings{
				Token:   *config.NewSecureString("test-token"),
				GroupID: 1,
			})
			bc.AllowFrom = tt.allow
			ch, messageBus := newVKHTTPTestChannel(t, bc, func(w http.ResponseWriter, _ *http.Request) {
				writeVKResponse(t, w, map[string]any{
					"response": []map[string]string{{"first_name": "Ada", "last_name": "Lovelace"}},
				})
			})

			ch.handleMessage(tt.message)
			assertNoVKInbound(t, messageBus)
		})
	}
}

func TestVKChannel_HandleMessageGroupTriggerCanIgnore(t *testing.T) {
	bc := makeVKTestBaseChannel(config.VKSettings{
		Token:   *config.NewSecureString("test-token"),
		GroupID: 1,
	})
	bc.GroupTrigger = config.GroupTriggerConfig{MentionOnly: true}
	ch, messageBus := newVKHTTPTestChannel(t, bc, func(w http.ResponseWriter, _ *http.Request) {
		writeVKResponse(t, w, map[string]any{
			"response": []map[string]string{{"first_name": "Ada", "last_name": "Lovelace"}},
		})
	})

	ch.handleMessage(object.MessagesMessage{FromID: 42, PeerID: 2000000001, Text: "hello"})
	assertNoVKInbound(t, messageBus)
}

func TestVKChannel_StartRequiresGroupID(t *testing.T) {
	ch, err := NewVKChannel("vk", makeVKTestBaseChannel(config.VKSettings{
		Token: *config.NewSecureString("test-token"),
	}), bus.NewMessageBus())
	if err != nil {
		t.Fatalf("NewVKChannel() error: %v", err)
	}

	if err := ch.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "group_id is required") {
		t.Fatalf("Start() error = %v, want missing group_id error", err)
	}
	if ch.IsRunning() {
		t.Fatal("Start() left channel running after validation failure")
	}
	select {
	case <-ch.ctx.Done():
	default:
		t.Fatal("Start() did not cancel its context after validation failure")
	}
}

func TestVKChannel_StartLongPollErrorCancelsContext(t *testing.T) {
	ch, _ := newVKHTTPTestChannel(t, makeVKTestBaseChannel(config.VKSettings{
		Token:   *config.NewSecureString("test-token"),
		GroupID: 1,
	}), func(w http.ResponseWriter, _ *http.Request) {
		writeVKResponse(t, w, map[string]any{
			"error": map[string]any{"error_code": 5, "error_msg": "invalid token"},
		})
	})

	if err := ch.Start(
		context.Background(),
	); err == nil ||
		!strings.Contains(err.Error(), "failed to create long poll") {
		t.Fatalf("Start() error = %v, want long poll creation error", err)
	}
	select {
	case <-ch.ctx.Done():
	default:
		t.Fatal("Start() did not cancel its context after long poll creation failure")
	}
}

func TestVKChannel_StartAndStopWithLocalLongPoll(t *testing.T) {
	pollStarted := make(chan struct{})
	var startOnce sync.Once
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/method/groups.getLongPollServer":
			writeVKResponse(t, w, map[string]any{
				"response": map[string]string{"key": "key", "server": server.URL + "/poll", "ts": "1"},
			})
		case "/method/groups.setLongPollSettings":
			writeVKResponse(t, w, map[string]any{"response": 1})
		case "/poll":
			startOnce.Do(func() { close(pollStarted) })
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	ch, err := NewVKChannel("vk", makeVKTestBaseChannel(config.VKSettings{
		Token:   *config.NewSecureString("test-token"),
		GroupID: 1,
	}), bus.NewMessageBus())
	if err != nil {
		t.Fatalf("NewVKChannel() error: %v", err)
	}
	vk := api.NewVK("test-token")
	vk.MethodURL = server.URL + "/method/"
	vk.Client = server.Client()
	vk.Limit = 1000
	ch.vk = vk

	if err := ch.Start(context.Background()); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	if !ch.IsRunning() {
		t.Fatal("Start() did not mark channel running")
	}
	select {
	case <-pollStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for local long poll request")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := ch.Stop(ctx); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	if ch.IsRunning() {
		t.Fatal("Stop() did not clear running state")
	}
	select {
	case <-ch.ctx.Done():
	default:
		t.Fatal("Stop() did not cancel channel context")
	}
}

func TestVKChannel_StopWithoutLongPoll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := &VKChannel{
		BaseChannel: channels.NewBaseChannel("vk", nil, bus.NewMessageBus(), nil),
		ctx:         ctx,
		cancel:      cancel,
	}
	ch.SetRunning(true)

	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	if ch.IsRunning() {
		t.Fatal("Stop() did not clear running state")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("Stop() did not cancel context")
	}
}
