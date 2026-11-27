package discord

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/sipeed/picoclaw/pkg/audio/tts"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
)

type stubTTSProvider struct{}

func (stubTTSProvider) Name() string { return "stub-tts" }

func (stubTTSProvider) Synthesize(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(&noopReader{}), nil
}

type noopReader struct{}

func (*noopReader) Read(p []byte) (int, error) {
	return 0, io.EOF
}

func TestApplyDiscordProxy_CustomProxy(t *testing.T) {
	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("discordgo.New() error: %v", err)
	}

	if err = applyDiscordProxy(session, "http://127.0.0.1:7890"); err != nil {
		t.Fatalf("applyDiscordProxy() error: %v", err)
	}

	req, err := http.NewRequest("GET", "https://discord.com/api/v10/gateway", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error: %v", err)
	}

	restProxy := session.Client.Transport.(*http.Transport).Proxy
	restProxyURL, err := restProxy(req)
	if err != nil {
		t.Fatalf("rest proxy func error: %v", err)
	}
	if got, want := restProxyURL.String(), "http://127.0.0.1:7890"; got != want {
		t.Fatalf("REST proxy = %q, want %q", got, want)
	}

	wsProxyURL, err := session.Dialer.Proxy(req)
	if err != nil {
		t.Fatalf("ws proxy func error: %v", err)
	}
	if got, want := wsProxyURL.String(), "http://127.0.0.1:7890"; got != want {
		t.Fatalf("WS proxy = %q, want %q", got, want)
	}
}

func TestApplyDiscordProxy_FromEnvironment(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:8888")
	t.Setenv("http_proxy", "http://127.0.0.1:8888")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:8888")
	t.Setenv("https_proxy", "http://127.0.0.1:8888")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("all_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("discordgo.New() error: %v", err)
	}

	if err = applyDiscordProxy(session, ""); err != nil {
		t.Fatalf("applyDiscordProxy() error: %v", err)
	}

	req, err := http.NewRequest("GET", "https://discord.com/api/v10/gateway", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error: %v", err)
	}

	gotURL, err := session.Dialer.Proxy(req)
	if err != nil {
		t.Fatalf("ws proxy func error: %v", err)
	}

	wantURL, err := url.Parse("http://127.0.0.1:8888")
	if err != nil {
		t.Fatalf("url.Parse() error: %v", err)
	}
	if gotURL.String() != wantURL.String() {
		t.Fatalf("WS proxy = %q, want %q", gotURL.String(), wantURL.String())
	}
}

func TestApplyDiscordProxy_InvalidProxyURL(t *testing.T) {
	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("discordgo.New() error: %v", err)
	}

	if err = applyDiscordProxy(session, "://bad-proxy"); err == nil {
		t.Fatal("applyDiscordProxy() expected error for invalid proxy URL, got nil")
	}
}

func TestSend_NonToolFeedbackDeletesTrackedProgressMessage(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()

		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/channels/chat-1/messages/prog-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"prog-1"}`)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	origChannels := discordgo.EndpointChannels
	discordgo.EndpointChannels = server.URL + "/channels/"
	defer func() {
		discordgo.EndpointChannels = origChannels
	}()

	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("discordgo.New() error: %v", err)
	}
	session.Client = server.Client()

	ch := &DiscordChannel{
		BaseChannel: channels.NewBaseChannel("discord", nil, bus.NewMessageBus(), nil),
		session:     session,
		ctx:         context.Background(),
		typingStop:  make(map[string]chan struct{}),
		voiceSSRC:   make(map[string]map[uint32]string),
	}
	ch.progress = channels.NewToolFeedbackAnimator(ch.EditMessage)
	ch.SetRunning(true)
	ch.RecordToolFeedbackMessage("chat-1", "prog-1", "🔧 `read_file`")

	ids, err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "chat-1",
		Content: "final reply",
		Context: bus.InboundContext{
			Channel: "discord",
			ChatID:  "chat-1",
		},
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if got, want := ids, []string{"prog-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Send() ids = %v, want %v", got, want)
	}
	if _, ok := ch.currentToolFeedbackMessage("chat-1"); ok {
		t.Fatal("expected tracked tool feedback message to be cleared")
	}

	mu.Lock()
	defer mu.Unlock()
	wantRequests := []string{
		"PATCH /channels/chat-1/messages/prog-1",
	}
	if !reflect.DeepEqual(requests, wantRequests) {
		t.Fatalf("requests = %v, want %v", requests, wantRequests)
	}
}

func TestEditMessage_UsesContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(time.Second):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"msg-1"}`)
		}
	}))
	defer server.Close()

	origChannels := discordgo.EndpointChannels
	discordgo.EndpointChannels = server.URL + "/channels/"
	defer func() {
		discordgo.EndpointChannels = origChannels
	}()

	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("discordgo.New() error: %v", err)
	}
	session.Client = server.Client()

	ch := &DiscordChannel{
		BaseChannel: channels.NewBaseChannel("discord", nil, bus.NewMessageBus(), nil),
		session:     session,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = ch.EditMessage(ctx, "chat-1", "msg-1", "still running")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected EditMessage() to fail when context times out")
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("EditMessage() ignored context timeout, elapsed=%v", elapsed)
	}
}

func TestFinalizeTrackedToolFeedbackMessage_StopsTrackingBeforeEdit(t *testing.T) {
	ch := &DiscordChannel{
		progress: channels.NewToolFeedbackAnimator(nil),
	}
	ch.RecordToolFeedbackMessage("chat-1", "msg-1", "🔧 `read_file`")

	msgIDs, handled := ch.finalizeTrackedToolFeedbackMessage(
		context.Background(),
		"chat-1",
		"final reply",
		func(_ context.Context, chatID, messageID, content string) error {
			if _, ok := ch.currentToolFeedbackMessage(chatID); ok {
				t.Fatal("expected tracked tool feedback to be stopped before edit")
			}
			if chatID != "chat-1" || messageID != "msg-1" || content != "final reply" {
				t.Fatalf("unexpected edit args: %s %s %s", chatID, messageID, content)
			}
			return nil
		},
	)
	if !handled {
		t.Fatal("expected finalizeTrackedToolFeedbackMessage to handle tracked message")
	}
	if got, want := msgIDs, []string{"msg-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("finalizeTrackedToolFeedbackMessage() ids = %v, want %v", got, want)
	}
}

func TestSend_NonToolFeedbackFinalizerStillStartsTTS(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()

		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/channels/chat-1/messages/prog-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"prog-1"}`)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	origChannels := discordgo.EndpointChannels
	discordgo.EndpointChannels = server.URL + "/channels/"
	defer func() {
		discordgo.EndpointChannels = origChannels
	}()

	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("discordgo.New() error: %v", err)
	}
	session.Client = server.Client()

	ttsStarted := make(chan string, 1)
	ch := &DiscordChannel{
		BaseChannel: channels.NewBaseChannel("discord", nil, bus.NewMessageBus(), nil),
		session:     session,
		ctx:         context.Background(),
		typingStop:  make(map[string]chan struct{}),
		voiceSSRC:   make(map[string]map[uint32]string),
		tts:         tts.TTSProvider(stubTTSProvider{}),
	}
	ch.ttsVoiceFn = func(string) (*discordgo.VoiceConnection, bool) {
		return &discordgo.VoiceConnection{}, true
	}
	ch.playTTSFn = func(_ context.Context, _ *discordgo.VoiceConnection, text string, _ uint64) {
		ttsStarted <- text
	}
	ch.progress = channels.NewToolFeedbackAnimator(ch.EditMessage)
	ch.SetRunning(true)
	ch.RecordToolFeedbackMessage("chat-1", "prog-1", "🔧 `read_file`")

	ids, err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "chat-1",
		Content: "final reply",
		Context: bus.InboundContext{
			Channel: "discord",
			ChatID:  "chat-1",
		},
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if got, want := ids, []string{"prog-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Send() ids = %v, want %v", got, want)
	}

	select {
	case got := <-ttsStarted:
		if got != "final reply" {
			t.Fatalf("TTS content = %q, want final reply", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected TTS to start for finalized tracked tool feedback reply")
	}
}

func TestHandleMessage_IgnoresDirectMessagesWhenDisabled(t *testing.T) {
	allowDM := false
	ch := &DiscordChannel{config: &config.DiscordSettings{AllowDM: &allowDM}}
	ch.handleMessage(nil, &discordgo.MessageCreate{Message: &discordgo.Message{
		GuildID: "",
		Author:  &discordgo.User{ID: "user-1"},
		Content: "hello",
	}})
}

func TestHandleMessage_InboundAttachmentsConfig(t *testing.T) {
	const attachmentURL = "://invalid-attachment-url"
	allowAttachments := true
	disallowAttachments := false

	tests := []struct {
		name              string
		allowAttachments  *bool
		content           string
		referencedContent string
		omitAttachment    bool
		wantInbound       bool
		wantContent       string
		wantMedia         []string
	}{
		{
			name:        "unset processes text and attachment",
			content:     "hello",
			wantInbound: true,
			wantContent: "hello\n[attachment: " + attachmentURL + "]",
			wantMedia:   []string{attachmentURL},
		},
		{
			name:             "explicit true processes text and attachment",
			allowAttachments: &allowAttachments,
			content:          "hello",
			wantInbound:      true,
			wantContent:      "hello\n[attachment: " + attachmentURL + "]",
			wantMedia:        []string{attachmentURL},
		},
		{
			name:             "false keeps text and ignores attachment",
			allowAttachments: &disallowAttachments,
			content:          "hello",
			wantInbound:      true,
			wantContent:      "hello",
			wantMedia:        []string{},
		},
		{
			name:             "false ignores attachment-only message",
			allowAttachments: &disallowAttachments,
			wantInbound:      false,
		},
		{
			name:              "false ignores attachment-only reply with quoted content",
			allowAttachments:  &disallowAttachments,
			referencedContent: "quoted hello",
			wantInbound:       false,
		},
		{
			name:              "false keeps quoted text when message has no attachments",
			allowAttachments:  &disallowAttachments,
			referencedContent: "quoted hello",
			omitAttachment:    true,
			wantInbound:       true,
			wantContent:       "[quoted message from quoted-user]: quoted hello\n\n",
			wantMedia:         []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messageBus := bus.NewMessageBus()
			session, err := discordgo.New("Bot test-token")
			if err != nil {
				t.Fatalf("discordgo.New() error = %v", err)
			}
			session.State.User = &discordgo.User{ID: "bot-1"}

			ch := &DiscordChannel{
				BaseChannel: channels.NewBaseChannel("discord", nil, messageBus, []string{"*"}),
				session:     session,
				config:      &config.DiscordSettings{AllowAttachments: tt.allowAttachments},
				ctx:         context.Background(),
			}

			message := &discordgo.Message{
				ID:        "msg-1",
				ChannelID: "chat-1",
				Author:    &discordgo.User{ID: "user-1", Username: "user"},
				Content:   tt.content,
			}
			if !tt.omitAttachment {
				message.Attachments = []*discordgo.MessageAttachment{{
					URL:      attachmentURL,
					Filename: "image.png",
				}}
			}
			if tt.referencedContent != "" {
				message.MessageReference = &discordgo.MessageReference{MessageID: "ref-1"}
				message.ReferencedMessage = &discordgo.Message{
					Content: tt.referencedContent,
					Author:  &discordgo.User{ID: "user-2", Username: "quoted-user"},
				}
			}

			ch.handleMessage(session, &discordgo.MessageCreate{Message: message})

			if !tt.wantInbound {
				select {
				case inbound := <-messageBus.InboundChan():
					t.Fatalf("unexpected inbound message: %#v", inbound)
				default:
					return
				}
			}

			select {
			case inbound := <-messageBus.InboundChan():
				if inbound.Content != tt.wantContent {
					t.Fatalf("inbound.Content = %q, want %q", inbound.Content, tt.wantContent)
				}
				if !reflect.DeepEqual(inbound.Media, tt.wantMedia) {
					t.Fatalf("inbound.Media = %#v, want %#v", inbound.Media, tt.wantMedia)
				}
			case <-time.After(time.Second):
				t.Fatal("timeout waiting for inbound message")
			}
		})
	}
}

func TestHandleMessage_DisabledAttachmentsSkipsDownload(t *testing.T) {
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer proxy.Close()

	messageBus := bus.NewMessageBus()
	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("discordgo.New() error = %v", err)
	}
	session.State.User = &discordgo.User{ID: "bot-1"}

	allowAttachments := false
	ch := &DiscordChannel{
		BaseChannel: channels.NewBaseChannel("discord", nil, messageBus, []string{"*"}),
		session:     session,
		config: &config.DiscordSettings{
			Proxy:            proxy.URL,
			AllowAttachments: &allowAttachments,
		},
		ctx: context.Background(),
	}

	ch.handleMessage(session, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "msg-1",
		ChannelID: "chat-1",
		Author:    &discordgo.User{ID: "user-1", Username: "user"},
		Content:   "hello",
		Attachments: []*discordgo.MessageAttachment{{
			URL:      "http://8.8.8.8/image.png",
			Filename: "image.png",
		}},
	}})

	if got := requests.Load(); got != 0 {
		t.Fatalf("attachment download requests = %d, want 0", got)
	}
	select {
	case inbound := <-messageBus.InboundChan():
		if inbound.Content != "hello" || len(inbound.Media) != 0 {
			t.Fatalf("unexpected inbound message: %#v", inbound)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for inbound message")
	}
}

func TestHandleInteractionCreate_IgnoresDirectMessagesWhenDisabled(t *testing.T) {
	allowDM := false
	ch := &DiscordChannel{config: &config.DiscordSettings{AllowDM: &allowDM}}
	ch.handleInteractionCreate(nil, &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:    discordgo.InteractionApplicationCommand,
		GuildID: "",
	}})
}
