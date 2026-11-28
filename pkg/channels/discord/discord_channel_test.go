package discord

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/commands"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
)

type capturedDiscordRequest struct {
	method string
	path   string
	body   []byte
}

func discordLocalHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{Transport: transport}
}

func newDiscordRESTTestChannel(t *testing.T, handler http.HandlerFunc) *DiscordChannel {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	originalChannels := discordgo.EndpointChannels
	discordgo.EndpointChannels = server.URL + "/channels/"
	t.Cleanup(func() { discordgo.EndpointChannels = originalChannels })

	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("discordgo.New() error: %v", err)
	}
	session.Client = discordLocalHTTPClient()

	messageBus := bus.NewMessageBus()
	ch := &DiscordChannel{
		BaseChannel: channels.NewBaseChannel("discord", nil, messageBus, nil),
		bc:          &config.Channel{},
		config:      &config.DiscordSettings{},
		session:     session,
		ctx:         context.Background(),
		typingStop:  make(map[string]chan struct{}),
		bus:         messageBus,
		voiceSSRC:   make(map[string]map[uint32]string),
	}
	ch.progress = channels.NewToolFeedbackAnimator(ch.EditMessage)
	return ch
}

func receiveDiscordInbound(t *testing.T, messageBus *bus.MessageBus) bus.InboundMessage {
	t.Helper()
	select {
	case inbound := <-messageBus.InboundChan():
		return inbound
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for inbound Discord message")
		return bus.InboundMessage{}
	}
}

func assertNoDiscordInbound(t *testing.T, messageBus *bus.MessageBus) {
	t.Helper()
	select {
	case inbound := <-messageBus.InboundChan():
		t.Fatalf("unexpected inbound Discord message: %+v", inbound)
	default:
	}
}

func TestNewDiscordChannel(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")

	messageBus := bus.NewMessageBus()
	cfg := &config.DiscordSettings{Token: *config.NewSecureString("test-token")}
	ch, err := NewDiscordChannel(
		&config.Channel{},
		cfg,
		messageBus,
		nil,
		WithCommandDefinitionsProvider(func() []commands.Definition {
			return []commands.Definition{{Name: "custom", Description: "Custom command"}}
		}),
	)
	if err != nil {
		t.Fatalf("NewDiscordChannel() error: %v", err)
	}
	if ch.Name() != "discord" || ch.MaxMessageLength() != 2000 || ch.IsRunning() {
		t.Fatalf(
			"unexpected new channel state: name=%q max=%d running=%t",
			ch.Name(),
			ch.MaxMessageLength(),
			ch.IsRunning(),
		)
	}
	if ch.session == nil || ch.config != cfg || ch.bus != messageBus {
		t.Fatal("NewDiscordChannel() did not retain its dependencies")
	}
	if ch.typingStop == nil || ch.voiceSSRC == nil || ch.progress == nil {
		t.Fatal("NewDiscordChannel() did not initialize channel state")
	}
	if got := ch.commandDefinitions(); len(got) != 1 || got[0].Name != "custom" {
		t.Fatalf("commandDefinitions() = %+v, want provider definition", got)
	}
}

func TestNewDiscordChannel_InvalidProxy(t *testing.T) {
	_, err := NewDiscordChannel(&config.Channel{}, &config.DiscordSettings{
		Token: *config.NewSecureString("test-token"),
		Proxy: "://bad-proxy",
	}, bus.NewMessageBus())
	if err == nil || !strings.Contains(err.Error(), "invalid discord proxy URL") {
		t.Fatalf("NewDiscordChannel() error = %v, want invalid proxy error", err)
	}
}

func TestDiscordChannel_SendValidation(t *testing.T) {
	ch := &DiscordChannel{
		BaseChannel: channels.NewBaseChannel("discord", nil, bus.NewMessageBus(), nil),
		progress:    channels.NewToolFeedbackAnimator(nil),
	}

	if _, err := ch.Send(
		context.Background(),
		bus.OutboundMessage{ChatID: "chat", Content: "hello"},
	); !errors.Is(
		err,
		channels.ErrNotRunning,
	) {
		t.Fatalf("Send() while stopped error = %v, want ErrNotRunning", err)
	}
	ch.SetRunning(true)
	if _, err := ch.Send(
		context.Background(),
		bus.OutboundMessage{Content: "hello"},
	); err == nil ||
		!strings.Contains(err.Error(), "channel ID is empty") {
		t.Fatalf("Send() empty chat error = %v, want channel ID error", err)
	}
	ids, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "chat"})
	if err != nil || len(ids) != 0 {
		t.Fatalf("Send() empty content = (%v, %v), want no ids and no error", ids, err)
	}
}

func TestDiscordChannel_SendAndReply(t *testing.T) {
	for _, tt := range []struct {
		name      string
		replyToID string
		wantRefID string
		wantRefCh string
	}{
		{name: "plain message"},
		{name: "reply", replyToID: "original-1", wantRefID: "original-1", wantRefCh: "chat-1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := make(chan capturedDiscordRequest, 1)
			ch := newDiscordRESTTestChannel(t, func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request body: %v", err)
				}
				requests <- capturedDiscordRequest{method: r.Method, path: r.URL.Path, body: body}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"sent-1"}`)
			})
			ch.SetRunning(true)

			ids, err := ch.Send(context.Background(), bus.OutboundMessage{
				ChatID:           "chat-1",
				Content:          "hello",
				ReplyToMessageID: tt.replyToID,
			})
			if err != nil {
				t.Fatalf("Send() error: %v", err)
			}
			if got, want := ids, []string{"sent-1"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("Send() ids = %v, want %v", got, want)
			}

			request := <-requests
			if request.method != http.MethodPost || request.path != "/channels/chat-1/messages" {
				t.Fatalf("request = %s %s, want POST /channels/chat-1/messages", request.method, request.path)
			}
			var payload struct {
				Content   string                      `json:"content"`
				Reference *discordgo.MessageReference `json:"message_reference"`
			}
			if err := json.Unmarshal(request.body, &payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if payload.Content != "hello" {
				t.Fatalf("content = %q, want hello", payload.Content)
			}
			if tt.wantRefID == "" {
				if payload.Reference != nil {
					t.Fatalf("reference = %+v, want none", payload.Reference)
				}
				return
			}
			if payload.Reference == nil || payload.Reference.MessageID != tt.wantRefID ||
				payload.Reference.ChannelID != tt.wantRefCh {
				t.Fatalf("reference = %+v, want message=%q channel=%q", payload.Reference, tt.wantRefID, tt.wantRefCh)
			}
		})
	}
}

func TestDiscordChannel_SendAPIError(t *testing.T) {
	ch := newDiscordRESTTestChannel(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"code":0,"message":"boom"}`)
	})
	ch.SetRunning(true)

	ids, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "chat-1", Content: "hello"})
	if !errors.Is(err, channels.ErrTemporary) {
		t.Fatalf("Send() error = %v, want ErrTemporary", err)
	}
	if len(ids) != 0 {
		t.Fatalf("Send() ids = %v, want none", ids)
	}
}

func TestDiscordChannel_SendMediaValidation(t *testing.T) {
	ch := &DiscordChannel{
		BaseChannel: channels.NewBaseChannel("discord", nil, bus.NewMessageBus(), nil),
	}
	if _, err := ch.SendMedia(
		context.Background(),
		bus.OutboundMediaMessage{ChatID: "chat"},
	); !errors.Is(
		err,
		channels.ErrNotRunning,
	) {
		t.Fatalf("SendMedia() while stopped error = %v, want ErrNotRunning", err)
	}
	ch.SetRunning(true)
	if _, err := ch.SendMedia(
		context.Background(),
		bus.OutboundMediaMessage{Parts: []bus.MediaPart{{Ref: "media://x"}}},
	); err == nil ||
		!strings.Contains(err.Error(), "channel ID is empty") {
		t.Fatalf("SendMedia() empty chat error = %v, want channel ID error", err)
	}
	if _, err := ch.SendMedia(
		context.Background(),
		bus.OutboundMediaMessage{ChatID: "chat"},
	); !errors.Is(
		err,
		channels.ErrSendFailed,
	) {
		t.Fatalf("SendMedia() without store error = %v, want ErrSendFailed", err)
	}

	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)
	ids, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "chat",
		Parts:  []bus.MediaPart{{Ref: "media://missing"}},
	})
	if err != nil || len(ids) != 0 {
		t.Fatalf("SendMedia() unresolved refs = (%v, %v), want no ids and no error", ids, err)
	}
}

func TestDiscordChannel_SendMedia(t *testing.T) {
	type receivedMedia struct {
		caption  string
		filename string
		content  string
		mimeType string
	}
	received := make(chan receivedMedia, 1)
	ch := newDiscordRESTTestChannel(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm(): %v", err)
			http.Error(w, "bad multipart", http.StatusBadRequest)
			return
		}
		payloads := r.MultipartForm.Value["payload_json"]
		if len(payloads) != 1 {
			t.Errorf("payload_json count = %d, want 1", len(payloads))
		}
		files := r.MultipartForm.File["files[0]"]
		if len(files) != 1 {
			t.Errorf("file count = %d, want 1", len(files))
			http.Error(w, "missing file", http.StatusBadRequest)
			return
		}
		file, err := files[0].Open()
		if err != nil {
			t.Errorf("open uploaded file: %v", err)
			http.Error(w, "bad file", http.StatusBadRequest)
			return
		}
		defer file.Close()
		content, err := io.ReadAll(file)
		if err != nil {
			t.Errorf("read uploaded file: %v", err)
		}
		var payload struct {
			Content string `json:"content"`
		}
		if len(payloads) == 1 {
			if err := json.Unmarshal([]byte(payloads[0]), &payload); err != nil {
				t.Errorf("decode payload: %v", err)
			}
		}
		received <- receivedMedia{
			caption:  payload.Content,
			filename: files[0].Filename,
			content:  string(content),
			mimeType: files[0].Header.Get("Content-Type"),
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"media-1"}`)
	})
	ch.SetRunning(true)

	path := t.TempDir() + "/image.png"
	if err := os.WriteFile(path, []byte("image-bytes"), 0o600); err != nil {
		t.Fatalf("write media file: %v", err)
	}
	store := media.NewFileMediaStore()
	ref, err := store.Store(path, media.MediaMeta{Filename: "image.png", ContentType: "image/png"}, "test")
	if err != nil {
		t.Fatalf("Store() error: %v", err)
	}
	ch.SetMediaStore(store)

	ids, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "chat-1",
		Parts: []bus.MediaPart{
			{Ref: "media://missing"},
			{Ref: ref, Filename: "image.png", ContentType: "image/png", Caption: "look"},
		},
	})
	if err != nil {
		t.Fatalf("SendMedia() error: %v", err)
	}
	if got, want := ids, []string{"media-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SendMedia() ids = %v, want %v", got, want)
	}
	got := <-received
	if got.caption != "look" || got.filename != "image.png" || got.content != "image-bytes" ||
		got.mimeType != "image/png" {
		t.Fatalf("received media = %+v", got)
	}
}

func TestDiscordChannel_SendMediaAPIError(t *testing.T) {
	ch := newDiscordRESTTestChannel(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	ch.SetRunning(true)

	path := t.TempDir() + "/file.txt"
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatalf("write media file: %v", err)
	}
	store := media.NewFileMediaStore()
	ref, err := store.Store(path, media.MediaMeta{Filename: "file.txt"}, "test")
	if err != nil {
		t.Fatalf("Store() error: %v", err)
	}
	ch.SetMediaStore(store)

	if _, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "chat-1",
		Parts:  []bus.MediaPart{{Ref: ref, Filename: "file.txt"}},
	}); !errors.Is(err, channels.ErrTemporary) {
		t.Fatalf("SendMedia() error = %v, want ErrTemporary", err)
	}
}

func TestDiscordChannel_EditDeleteAndPlaceholder(t *testing.T) {
	requests := make(chan capturedDiscordRequest, 3)
	ch := newDiscordRESTTestChannel(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		requests <- capturedDiscordRequest{method: r.Method, path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = io.WriteString(w, `{"id":"placeholder-1"}`)
	})

	if id, err := ch.SendPlaceholder(context.Background(), "chat-1"); err != nil || id != "" {
		t.Fatalf("SendPlaceholder() disabled = (%q, %v), want empty id and no error", id, err)
	}
	ch.bc.Placeholder = config.PlaceholderConfig{Enabled: true, Text: config.FlexibleStringSlice{"Working"}}
	if err := ch.EditMessage(context.Background(), "chat-1", "message-1", "edited"); err != nil {
		t.Fatalf("EditMessage() error: %v", err)
	}
	if err := ch.DeleteMessage(context.Background(), "chat-1", "message-1"); err != nil {
		t.Fatalf("DeleteMessage() error: %v", err)
	}
	if id, err := ch.SendPlaceholder(context.Background(), "chat-1"); err != nil || id != "placeholder-1" {
		t.Fatalf("SendPlaceholder() = (%q, %v), want placeholder-1", id, err)
	}

	want := []capturedDiscordRequest{
		{method: http.MethodPatch, path: "/channels/chat-1/messages/message-1"},
		{method: http.MethodDelete, path: "/channels/chat-1/messages/message-1"},
		{method: http.MethodPost, path: "/channels/chat-1/messages"},
	}
	for i, expected := range want {
		select {
		case got := <-requests:
			if got.method != expected.method || got.path != expected.path {
				t.Errorf("request %d = %s %s, want %s %s", i, got.method, got.path, expected.method, expected.path)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for request %d", i)
		}
	}
}

func newDiscordInboundTestChannel(
	t *testing.T,
	allowFrom []string,
	groupTrigger config.GroupTriggerConfig,
) (*DiscordChannel, *bus.MessageBus) {
	t.Helper()
	messageBus := bus.NewMessageBus()
	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("discordgo.New() error: %v", err)
	}
	session.State = discordgo.NewState()
	session.State.User = &discordgo.User{ID: "bot-1", Username: "pico"}
	ch := &DiscordChannel{
		BaseChannel: channels.NewBaseChannel(
			"discord",
			nil,
			messageBus,
			allowFrom,
			channels.WithGroupTrigger(groupTrigger),
		),
		bc:         &config.Channel{},
		config:     &config.DiscordSettings{},
		session:    session,
		ctx:        context.Background(),
		typingStop: make(map[string]chan struct{}),
		bus:        messageBus,
		voiceSSRC:  make(map[string]map[uint32]string),
		botUserID:  "bot-1",
	}
	return ch, messageBus
}

func discordInboundMessage(content string) *discordgo.MessageCreate {
	return &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "message-1",
		ChannelID: "chat-1",
		Content:   content,
		Author: &discordgo.User{
			ID:            "user-1",
			Username:      "alice",
			Discriminator: "1234",
		},
	}}
}

func TestDiscordChannel_HandleMessageDirect(t *testing.T) {
	ch, messageBus := newDiscordInboundTestChannel(t, nil, config.GroupTriggerConfig{})
	message := discordInboundMessage("<@bot-1> hello")
	ch.handleMessage(ch.session, message)

	inbound := receiveDiscordInbound(t, messageBus)
	if inbound.Content != "hello" || inbound.Context.ChatID != "chat-1" || inbound.Context.ChatType != "direct" {
		t.Fatalf("unexpected inbound message: %+v", inbound)
	}
	if inbound.Context.SenderID != "user-1" || inbound.Context.MessageID != "message-1" || inbound.Context.Mentioned {
		t.Fatalf("unexpected inbound context: %+v", inbound.Context)
	}
	if inbound.Sender.DisplayName != "alice#1234" || inbound.Sender.CanonicalID != "discord:user-1" {
		t.Fatalf("unexpected sender: %+v", inbound.Sender)
	}
	if inbound.Context.Raw["is_dm"] != "true" || inbound.MediaScope != "discord:chat-1:message-1" {
		t.Fatalf("unexpected metadata/scope: %+v / %q", inbound.Context.Raw, inbound.MediaScope)
	}
}

func TestDiscordChannel_HandleMessageGuildMentionAndQuote(t *testing.T) {
	ch, messageBus := newDiscordInboundTestChannel(t, nil, config.GroupTriggerConfig{MentionOnly: true})
	message := discordInboundMessage("<@!bot-1> status")
	message.GuildID = "guild-1"
	message.Mentions = []*discordgo.User{{ID: "bot-1"}}
	message.MessageReference = &discordgo.MessageReference{MessageID: "original-1", ChannelID: "chat-1"}
	message.ReferencedMessage = &discordgo.Message{
		Content: "original request",
		Author:  &discordgo.User{Username: "bob"},
	}
	ch.handleMessage(ch.session, message)

	inbound := receiveDiscordInbound(t, messageBus)
	if got, want := inbound.Content, "[quoted message from bob]: original request\n\nstatus"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if !inbound.Context.Mentioned || inbound.Context.ChatType != "channel" {
		t.Fatalf("unexpected guild context: %+v", inbound.Context)
	}
	if inbound.Context.SpaceID != "guild-1" || inbound.Context.SpaceType != "guild" ||
		inbound.Context.ReplyToMessageID != "original-1" {
		t.Fatalf("unexpected guild/reply metadata: %+v", inbound.Context)
	}
}

func TestDiscordChannel_HandleMessageIgnoresUnwantedMessages(t *testing.T) {
	t.Run("nil author", func(t *testing.T) {
		ch, messageBus := newDiscordInboundTestChannel(t, nil, config.GroupTriggerConfig{})
		message := discordInboundMessage("hello")
		message.Author = nil
		ch.handleMessage(ch.session, message)
		assertNoDiscordInbound(t, messageBus)
	})
	t.Run("own message", func(t *testing.T) {
		ch, messageBus := newDiscordInboundTestChannel(t, nil, config.GroupTriggerConfig{})
		message := discordInboundMessage("hello")
		message.Author.ID = "bot-1"
		ch.handleMessage(ch.session, message)
		assertNoDiscordInbound(t, messageBus)
	})
	t.Run("empty", func(t *testing.T) {
		ch, messageBus := newDiscordInboundTestChannel(t, nil, config.GroupTriggerConfig{})
		ch.handleMessage(ch.session, discordInboundMessage(""))
		assertNoDiscordInbound(t, messageBus)
	})
	t.Run("unauthorized", func(t *testing.T) {
		ch, messageBus := newDiscordInboundTestChannel(t, []string{"other-user"}, config.GroupTriggerConfig{})
		ch.handleMessage(ch.session, discordInboundMessage("hello"))
		assertNoDiscordInbound(t, messageBus)
	})
	t.Run("group trigger", func(t *testing.T) {
		ch, messageBus := newDiscordInboundTestChannel(t, nil, config.GroupTriggerConfig{MentionOnly: true})
		message := discordInboundMessage("hello")
		message.GuildID = "guild-1"
		ch.handleMessage(ch.session, message)
		assertNoDiscordInbound(t, messageBus)
	})
}

func TestDiscordChannel_HandleMessageAttachmentDownloadFailure(t *testing.T) {
	ch, messageBus := newDiscordInboundTestChannel(t, nil, config.GroupTriggerConfig{})
	const attachmentURL = "http://127.0.0.1:1/blocked.png"
	message := discordInboundMessage("")
	message.Attachments = []*discordgo.MessageAttachment{{
		URL:         attachmentURL,
		Filename:    "blocked.png",
		ContentType: "image/png",
	}}
	ch.handleMessage(ch.session, message)

	inbound := receiveDiscordInbound(t, messageBus)
	if got, want := inbound.Content, "[attachment: "+attachmentURL+"]"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if got, want := inbound.Media, []string{attachmentURL}; !reflect.DeepEqual(got, want) {
		t.Fatalf("media = %v, want %v", got, want)
	}
}

func TestDiscordChannel_ResolveDiscordRefs(t *testing.T) {
	requests := make(chan string, 4)
	ch := newDiscordRESTTestChannel(t, func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/channels/20":
			_, _ = io.WriteString(w, `{"id":"20","name":"api-channel"}`)
		case "/channels/30/messages/40":
			_, _ = io.WriteString(w, `{"id":"40","content":"linked","author":{"username":"bob"}}`)
		default:
			http.NotFound(w, r)
		}
	})
	if err := ch.session.State.GuildAdd(&discordgo.Guild{ID: "guild-1"}); err != nil {
		t.Fatalf("GuildAdd() error: %v", err)
	}
	if err := ch.session.State.ChannelAdd(
		&discordgo.Channel{ID: "10", Name: "general", GuildID: "guild-1"},
	); err != nil {
		t.Fatalf("ChannelAdd() error: %v", err)
	}

	text := "<#10> <#20> <#99> " +
		"https://discord.com/channels/111/30/40 " +
		"https://discord.com/channels/222/30/41"
	got := ch.resolveDiscordRefs(ch.session, text, "111")
	want := "#general #api-channel <#99> https://discord.com/channels/111/30/40 " +
		"https://discord.com/channels/222/30/41\n[linked message from bob]: linked"
	if got != want {
		t.Fatalf("resolveDiscordRefs() = %q, want %q", got, want)
	}

	var paths []string
	for len(requests) > 0 {
		paths = append(paths, <-requests)
	}
	wantPaths := []string{"/channels/20", "/channels/99", "/channels/30/messages/40"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("requests = %v, want %v", paths, wantPaths)
	}
}

func TestDiscordChannel_StripBotMention(t *testing.T) {
	ch := &DiscordChannel{botUserID: "bot-1"}
	for _, tt := range []struct {
		input string
		want  string
	}{
		{input: "<@bot-1> hello", want: "hello"},
		{input: "<@!bot-1> hello", want: "hello"},
		{input: "<@other> hello", want: "<@other> hello"},
	} {
		if got := ch.stripBotMention(tt.input); got != tt.want {
			t.Errorf("stripBotMention(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
	ch.botUserID = ""
	if got := ch.stripBotMention("  hello  "); got != "  hello  " {
		t.Fatalf("stripBotMention() without bot id = %q, want unchanged", got)
	}
}

func TestAttachmentMediaTag(t *testing.T) {
	for _, tt := range []struct {
		filename    string
		contentType string
		want        string
	}{
		{filename: "image.png", contentType: "IMAGE/PNG", want: "image"},
		{filename: "voice.ogg", contentType: "application/ogg", want: "audio"},
		{filename: "clip.mp4", contentType: "video/mp4", want: "video"},
		{filename: "PHOTO.JPG", want: "image"},
		{filename: "sound.mp3", want: "audio"},
		{filename: "movie.webm", want: "video"},
		{filename: "archive.zip", contentType: "application/zip", want: "file"},
	} {
		if got := attachmentMediaTag(tt.filename, tt.contentType); got != tt.want {
			t.Errorf("attachmentMediaTag(%q, %q) = %q, want %q", tt.filename, tt.contentType, got, tt.want)
		}
	}
}

func TestDiscordChannel_StartTypingAndStop(t *testing.T) {
	typingRequests := make(chan struct{}, 1)
	ch := newDiscordRESTTestChannel(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/channels/chat-1/typing" {
			t.Errorf("typing request = %s %s", r.Method, r.URL.Path)
		}
		typingRequests <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	})
	ctx, cancel := context.WithCancel(context.Background())
	ch.ctx = ctx
	defer cancel()

	stop, err := ch.StartTyping(context.Background(), "chat-1")
	if err != nil {
		t.Fatalf("StartTyping() error: %v", err)
	}
	select {
	case <-typingRequests:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for typing request")
	}
	stop()
	stop()
	ch.typingMu.Lock()
	remaining := len(ch.typingStop)
	ch.typingMu.Unlock()
	if remaining != 0 {
		t.Fatalf("typing loops remaining = %d, want 0", remaining)
	}
}

func TestDiscordChannel_Stop(t *testing.T) {
	ch := newDiscordRESTTestChannel(t, func(http.ResponseWriter, *http.Request) {
		t.Error("unexpected HTTP request during Stop")
	})
	ctx, cancel := context.WithCancel(context.Background())
	ch.ctx = ctx
	ch.cancel = cancel
	ch.SetRunning(true)
	typingStop := make(chan struct{})
	ch.typingStop["chat-1"] = typingStop
	ch.RecordToolFeedbackMessage("chat-1", "message-1", "working")

	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	if ch.IsRunning() {
		t.Fatal("Stop() did not clear running state")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("Stop() did not cancel channel context")
	}
	select {
	case <-typingStop:
	default:
		t.Fatal("Stop() did not close typing stop channel")
	}
	if len(ch.typingStop) != 0 {
		t.Fatalf("typingStop entries = %d, want 0", len(ch.typingStop))
	}
	if _, ok := ch.currentToolFeedbackMessage("chat-1"); ok {
		t.Fatal("Stop() did not clear tool feedback state")
	}
}

func TestDiscordChannel_StartUserLookupError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/@me" {
			t.Errorf("request path = %q, want /users/@me", r.URL.Path)
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()
	originalUsers := discordgo.EndpointUsers
	discordgo.EndpointUsers = server.URL + "/users/"
	defer func() { discordgo.EndpointUsers = originalUsers }()

	ch, err := NewDiscordChannel(
		&config.Channel{},
		&config.DiscordSettings{Token: *config.NewSecureString("test-token")},
		bus.NewMessageBus(),
	)
	if err != nil {
		t.Fatalf("NewDiscordChannel() error: %v", err)
	}
	ch.session.Client = discordLocalHTTPClient()
	if err := ch.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "failed to get bot user") {
		t.Fatalf("Start() error = %v, want bot user lookup error", err)
	}
	if ch.cancel != nil {
		ch.cancel()
	}
	if ch.IsRunning() {
		t.Fatal("Start() left channel running after user lookup failure")
	}
}

func TestDiscordChannel_VoiceConnectionForTTS(t *testing.T) {
	ch := &DiscordChannel{}
	if _, ok := ch.voiceConnectionForTTS("chat"); ok {
		t.Fatal("voiceConnectionForTTS() without session should fail")
	}
	ch.session = &discordgo.Session{State: discordgo.NewState()}
	ch.session.VoiceConnections = make(map[string]*discordgo.VoiceConnection)
	if _, ok := ch.voiceConnectionForTTS("chat"); ok {
		t.Fatal("voiceConnectionForTTS() without cached channel should fail")
	}
	if err := ch.session.State.GuildAdd(&discordgo.Guild{ID: "guild-1"}); err != nil {
		t.Fatalf("GuildAdd() error: %v", err)
	}
	if err := ch.session.State.ChannelAdd(&discordgo.Channel{ID: "chat", GuildID: "guild-1"}); err != nil {
		t.Fatalf("ChannelAdd() error: %v", err)
	}
	if _, ok := ch.voiceConnectionForTTS("chat"); ok {
		t.Fatal("voiceConnectionForTTS() without voice connection should fail")
	}
	vc := &discordgo.VoiceConnection{}
	ch.session.VoiceConnections["guild-1"] = vc
	if got, ok := ch.voiceConnectionForTTS("chat"); !ok || got != vc {
		t.Fatalf("voiceConnectionForTTS() = (%p, %t), want (%p, true)", got, ok, vc)
	}
}

func TestDiscordChannel_VoiceCapabilities(t *testing.T) {
	ch := &DiscordChannel{}
	caps := ch.VoiceCapabilities()
	if !caps.ASR || !caps.TTS {
		t.Fatalf("VoiceCapabilities() = %+v, want ASR and TTS", caps)
	}
}

func TestDiscordChannel_HandleInteractionCreate(t *testing.T) {
	requests := make(chan capturedDiscordRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read interaction body: %v", err)
		}
		requests <- capturedDiscordRequest{method: r.Method, path: r.URL.Path, body: body}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	originalAPI := discordgo.EndpointAPI
	discordgo.EndpointAPI = server.URL + "/api/"
	defer func() { discordgo.EndpointAPI = originalAPI }()

	messageBus := bus.NewMessageBus()
	ch := &DiscordChannel{
		BaseChannel: channels.NewBaseChannel("discord", nil, messageBus, []string{"user-1"}),
		ctx:         context.Background(),
	}
	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("discordgo.New() error: %v", err)
	}
	session.Client = discordLocalHTTPClient()
	ch.session = session

	interaction := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID:        "interaction-1",
		Token:     "token-1",
		Type:      discordgo.InteractionApplicationCommand,
		GuildID:   "guild-1",
		ChannelID: "chat-1",
		Member:    &discordgo.Member{User: &discordgo.User{ID: "user-1", Username: "alice"}},
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "help",
			Options: []*discordgo.ApplicationCommandInteractionDataOption{{
				Name:  discordCommandArgumentsOption,
				Type:  discordgo.ApplicationCommandOptionString,
				Value: "commands",
			}},
		},
	}}
	ch.handleInteractionCreate(session, interaction)

	request := <-requests
	if request.method != http.MethodPost || request.path != "/api/interactions/interaction-1/token-1/callback" {
		t.Fatalf("interaction request = %s %s", request.method, request.path)
	}
	var response discordgo.InteractionResponse
	if err := json.Unmarshal(request.body, &response); err != nil {
		t.Fatalf("decode interaction response: %v", err)
	}
	if response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("interaction response type = %d, want deferred", response.Type)
	}
	inbound := receiveDiscordInbound(t, messageBus)
	if inbound.Content != "/help commands" || !inbound.Context.Mentioned || inbound.Context.SpaceID != "guild-1" {
		t.Fatalf("unexpected interaction inbound: %+v", inbound)
	}

	interaction.Member.User.ID = "blocked-user"
	ch.handleInteractionCreate(session, interaction)
	request = <-requests
	if err := json.Unmarshal(request.body, &response); err != nil {
		t.Fatalf("decode rejection response: %v", err)
	}
	if response.Type != discordgo.InteractionResponseChannelMessageWithSource || response.Data == nil ||
		response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("unexpected rejection response: %+v", response)
	}
	assertNoDiscordInbound(t, messageBus)
}
