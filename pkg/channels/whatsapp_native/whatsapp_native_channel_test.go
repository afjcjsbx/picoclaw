//go:build whatsapp_native

package whatsapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
)

func newWhatsAppInboundTestChannel(allowFrom []string) (*WhatsAppNativeChannel, *bus.MessageBus) {
	messageBus := bus.NewMessageBus()
	cfg := &config.WhatsAppSettings{}
	return &WhatsAppNativeChannel{
		BaseChannel: channels.NewBaseChannel("whatsapp_native", cfg, messageBus, allowFrom),
		config:      cfg,
		runCtx:      context.Background(),
	}, messageBus
}

func whatsAppTestEvent(sender, chat types.JID, message *waE2E.Message) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Sender: sender, Chat: chat},
			ID:            "message-1",
			PushName:      "Alice",
		},
		Message: message,
	}
}

func receiveWhatsAppInbound(t *testing.T, messageBus *bus.MessageBus) bus.InboundMessage {
	t.Helper()
	select {
	case inbound := <-messageBus.InboundChan():
		return inbound
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for inbound WhatsApp message")
		return bus.InboundMessage{}
	}
}

func assertNoWhatsAppInbound(t *testing.T, messageBus *bus.MessageBus) {
	t.Helper()
	select {
	case inbound := <-messageBus.InboundChan():
		t.Fatalf("unexpected inbound WhatsApp message: %+v", inbound)
	default:
	}
}

func TestNewWhatsAppNativeChannel(t *testing.T) {
	for _, tt := range []struct {
		name      string
		storePath string
		wantPath  string
	}{
		{name: "default store path", wantPath: "whatsapp"},
		{name: "custom store path", storePath: "custom-whatsapp", wantPath: "custom-whatsapp"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			messageBus := bus.NewMessageBus()
			created, err := NewWhatsAppNativeChannel(
				&config.Channel{},
				"whatsapp_native",
				&config.WhatsAppSettings{},
				messageBus,
				tt.storePath,
			)
			if err != nil {
				t.Fatalf("NewWhatsAppNativeChannel() error: %v", err)
			}
			ch, ok := created.(*WhatsAppNativeChannel)
			if !ok {
				t.Fatalf("created channel type = %T, want *WhatsAppNativeChannel", created)
			}
			if ch.Name() != "whatsapp_native" || ch.MaxMessageLength() != 65536 || ch.IsRunning() {
				t.Fatalf(
					"unexpected new channel state: name=%q max=%d running=%t",
					ch.Name(),
					ch.MaxMessageLength(),
					ch.IsRunning(),
				)
			}
			if ch.storePath != tt.wantPath || ch.config == nil {
				t.Fatalf("storePath/config = %q/%p, want %q and non-nil config", ch.storePath, ch.config, tt.wantPath)
			}
		})
	}
}

func TestWhatsAppNativeChannel_StartStorePathError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("file"), 0o600); err != nil {
		t.Fatalf("write store path file: %v", err)
	}
	ch, _ := newWhatsAppInboundTestChannel(nil)
	ch.storePath = path

	if err := ch.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "create session store dir") {
		t.Fatalf("Start() error = %v, want store directory error", err)
	}
	if ch.IsRunning() || ch.client != nil || ch.container != nil {
		t.Fatalf(
			"Start() left partial state after failure: running=%t client=%p container=%p",
			ch.IsRunning(),
			ch.client,
			ch.container,
		)
	}
}

func TestWhatsAppNativeChannel_StartCanceledContext(t *testing.T) {
	ch, _ := newWhatsAppInboundTestChannel(nil)
	ch.storePath = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := ch.Start(ctx); err == nil || !strings.Contains(err.Error(), "enable foreign keys") {
		t.Fatalf("Start() error = %v, want canceled database setup error", err)
	}
	if ch.IsRunning() || ch.client != nil || ch.container != nil {
		t.Fatalf(
			"Start() left partial state after cancellation: running=%t client=%p container=%p",
			ch.IsRunning(),
			ch.client,
			ch.container,
		)
	}
}

func TestWhatsAppNativeChannel_Stop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch, _ := newWhatsAppInboundTestChannel(nil)
	ch.runCtx = ctx
	ch.runCancel = cancel
	ch.client = whatsmeow.NewClient(&store.Device{}, nil)
	ch.SetRunning(true)

	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	if ch.IsRunning() || ch.client != nil || ch.container != nil {
		t.Fatalf(
			"Stop() left resources attached: running=%t client=%p container=%p",
			ch.IsRunning(),
			ch.client,
			ch.container,
		)
	}
	if !ch.stopping.Load() {
		t.Fatal("Stop() did not mark channel as stopping")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("Stop() did not cancel run context")
	}
}

func TestWhatsAppNativeChannel_StopWithoutResources(t *testing.T) {
	ch, _ := newWhatsAppInboundTestChannel(nil)
	ch.SetRunning(true)
	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	if ch.IsRunning() || !ch.stopping.Load() {
		t.Fatalf("Stop() state = running:%t stopping:%t", ch.IsRunning(), ch.stopping.Load())
	}
}

func TestWhatsAppNativeChannel_StopHonorsCanceledWaitContext(t *testing.T) {
	ch, _ := newWhatsAppInboundTestChannel(nil)
	ch.SetRunning(true)
	release := make(chan struct{})
	ch.wg.Add(1)
	go func() {
		defer ch.wg.Done()
		<-release
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := ch.Stop(ctx); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	close(release)
	if ch.IsRunning() {
		t.Fatal("Stop() did not clear running state after canceled wait")
	}
}

func TestWhatsAppNativeChannel_SendValidation(t *testing.T) {
	ch, _ := newWhatsAppInboundTestChannel(nil)
	if _, err := ch.Send(
		context.Background(),
		bus.OutboundMessage{ChatID: "15551234567", Content: "hello"},
	); !errors.Is(
		err,
		channels.ErrNotRunning,
	) {
		t.Fatalf("Send() while stopped error = %v, want ErrNotRunning", err)
	}

	ch.SetRunning(true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ch.Send(
		ctx,
		bus.OutboundMessage{ChatID: "15551234567", Content: "hello"},
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("Send() canceled context error = %v, want context.Canceled", err)
	}
	if _, err := ch.Send(
		context.Background(),
		bus.OutboundMessage{ChatID: "15551234567", Content: "hello"},
	); !errors.Is(
		err,
		channels.ErrTemporary,
	) {
		t.Fatalf("Send() without client error = %v, want ErrTemporary", err)
	}

	ch.client = whatsmeow.NewClient(&store.Device{}, nil)
	if _, err := ch.Send(
		context.Background(),
		bus.OutboundMessage{ChatID: "15551234567", Content: "hello"},
	); !errors.Is(
		err,
		channels.ErrTemporary,
	) {
		t.Fatalf("Send() disconnected client error = %v, want ErrTemporary", err)
	}
}

func TestParseJID(t *testing.T) {
	for _, tt := range []struct {
		name    string
		input   string
		want    types.JID
		wantErr bool
	}{
		{name: "phone number", input: "15551234567", want: types.NewJID("15551234567", types.DefaultUserServer)},
		{name: "trimmed phone number", input: " 15551234567 ", want: types.NewJID("15551234567", types.DefaultUserServer)},
		{name: "user jid", input: "15551234567@s.whatsapp.net", want: types.NewJID("15551234567", types.DefaultUserServer)},
		{name: "group jid", input: "120363000000000000@g.us", want: types.NewJID("120363000000000000", types.GroupServer)},
		{name: "device jid", input: "15551234567:3@s.whatsapp.net", want: types.JID{User: "15551234567", Device: 3, Server: types.DefaultUserServer}},
		{name: "empty", input: "", wantErr: true},
		{name: "whitespace", input: "  ", wantErr: true},
		{name: "invalid device", input: "15551234567:invalid@s.whatsapp.net", wantErr: true},
		{name: "invalid agent", input: "15551234567.1.2@s.whatsapp.net", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseJID(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseJID(%q) = %s, want error", tt.input, got.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("parseJID(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("parseJID(%q) = %s, want %s", tt.input, got.String(), tt.want.String())
			}
		})
	}
}

func TestWhatsAppNativeChannel_HandleIncomingDirectAndGroup(t *testing.T) {
	t.Run("direct conversation is sanitized", func(t *testing.T) {
		ch, messageBus := newWhatsAppInboundTestChannel(nil)
		jid := types.NewJID("15551234567", types.DefaultUserServer)
		ch.handleIncoming(whatsAppTestEvent(jid, jid, &waE2E.Message{
			Conversation: proto.String("\u200bhello\n"),
		}))

		inbound := receiveWhatsAppInbound(t, messageBus)
		if inbound.Content != "hello\n" || inbound.Context.ChatType != "direct" {
			t.Fatalf("unexpected direct inbound: %+v", inbound)
		}
		if inbound.Context.ChatID != jid.String() || inbound.Context.SenderID != jid.String() ||
			inbound.Context.MessageID != "message-1" {
			t.Fatalf("unexpected direct context: %+v", inbound.Context)
		}
		if inbound.Sender.CanonicalID != "whatsapp:"+jid.String() || inbound.Sender.DisplayName != "Alice" {
			t.Fatalf("unexpected sender: %+v", inbound.Sender)
		}
		if inbound.Context.Raw["peer_kind"] != "direct" || inbound.Context.Raw["peer_id"] != jid.String() ||
			inbound.Context.Raw["user_name"] != "Alice" {
			t.Fatalf("unexpected direct metadata: %+v", inbound.Context.Raw)
		}
	})

	t.Run("group extended text", func(t *testing.T) {
		ch, messageBus := newWhatsAppInboundTestChannel(nil)
		sender := types.NewJID("15551234567", types.DefaultUserServer)
		chat := types.NewJID("120363000000000000", types.GroupServer)
		ch.handleIncoming(whatsAppTestEvent(sender, chat, &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("group hello")},
		}))

		inbound := receiveWhatsAppInbound(t, messageBus)
		if inbound.Content != "group hello" || inbound.Context.ChatType != "group" ||
			inbound.Context.ChatID != chat.String() {
			t.Fatalf("unexpected group inbound: %+v", inbound)
		}
		if inbound.Context.Raw["peer_kind"] != "group" || inbound.Context.Raw["peer_id"] != chat.String() {
			t.Fatalf("unexpected group metadata: %+v", inbound.Context.Raw)
		}
	})
}

func TestWhatsAppNativeChannel_HandleIncomingIgnoresUnwantedMessages(t *testing.T) {
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	for _, tt := range []struct {
		name    string
		message *waE2E.Message
	}{
		{name: "nil protobuf message"},
		{name: "empty message", message: &waE2E.Message{}},
		{name: "media only", message: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}},
		{name: "only control characters", message: &waE2E.Message{Conversation: proto.String("\u200b\x00")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ch, messageBus := newWhatsAppInboundTestChannel(nil)
			ch.handleIncoming(whatsAppTestEvent(jid, jid, tt.message))
			assertNoWhatsAppInbound(t, messageBus)
		})
	}
}

func TestWhatsAppNativeChannel_HandleIncomingRejectsUnauthorizedSender(t *testing.T) {
	ch, messageBus := newWhatsAppInboundTestChannel([]string{"19995550000"})
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	ch.handleIncoming(whatsAppTestEvent(jid, jid, &waE2E.Message{Conversation: proto.String("hello")}))
	assertNoWhatsAppInbound(t, messageBus)
}

func TestWhatsAppNativeChannel_HandleIncomingCanceledContext(t *testing.T) {
	ch, messageBus := newWhatsAppInboundTestChannel(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch.runCtx = ctx
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	ch.handleIncoming(whatsAppTestEvent(jid, jid, &waE2E.Message{Conversation: proto.String("hello")}))
	assertNoWhatsAppInbound(t, messageBus)
}

func TestWhatsAppNativeChannel_EventHandlerRoutesMessages(t *testing.T) {
	ch, messageBus := newWhatsAppInboundTestChannel(nil)
	jid := types.NewJID("15551234567", types.DefaultUserServer)
	ch.eventHandler(whatsAppTestEvent(jid, jid, &waE2E.Message{Conversation: proto.String("hello")}))
	inbound := receiveWhatsAppInbound(t, messageBus)
	if inbound.Content != "hello" {
		t.Fatalf("content = %q, want hello", inbound.Content)
	}
	ch.eventHandler(nil)
}

func TestWhatsAppNativeChannel_EventHandlerDisconnectedWhileStopping(t *testing.T) {
	ch, _ := newWhatsAppInboundTestChannel(nil)
	ch.stopping.Store(true)
	ch.eventHandler(&events.Disconnected{})
	ch.wg.Wait()
	if ch.reconnecting {
		t.Fatal("disconnected event started reconnect while stopping")
	}
}

func TestWhatsAppNativeChannel_EventHandlerDisconnectedStartsCanceledReconnect(t *testing.T) {
	ch, _ := newWhatsAppInboundTestChannel(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch.runCtx = ctx

	ch.eventHandler(&events.Disconnected{})
	ch.wg.Wait()
	if ch.reconnecting {
		t.Fatal("canceled reconnect did not clear reconnecting state")
	}
}

func TestWhatsAppNativeChannel_EventHandlerIgnoresDuplicateDisconnect(t *testing.T) {
	ch, _ := newWhatsAppInboundTestChannel(nil)
	ch.reconnecting = true
	ch.eventHandler(&events.Disconnected{})
	ch.wg.Wait()
	if !ch.reconnecting {
		t.Fatal("duplicate disconnected event cleared an active reconnect")
	}
}

func TestWhatsAppNativeChannel_ReconnectWithoutClient(t *testing.T) {
	ch, _ := newWhatsAppInboundTestChannel(nil)
	ch.reconnecting = true
	ch.reconnectWithBackoff()
	if ch.reconnecting {
		t.Fatal("reconnectWithBackoff() did not reset reconnecting state")
	}
}
