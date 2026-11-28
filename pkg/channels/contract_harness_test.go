package channels

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
)

const contractChannelType = "contract-test-channel"

type contractChannel struct {
	*BaseChannel

	mu           sync.Mutex
	starts       int
	stops        int
	sent         []bus.OutboundMessage
	media        []bus.OutboundMediaMessage
	startErr     error
	sendErr      error
	mediaSendErr error
}

func (c *contractChannel) Start(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.starts++
	if c.startErr != nil {
		return c.startErr
	}
	c.SetRunning(true)
	return nil
}

func (c *contractChannel) Stop(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stops++
	c.SetRunning(false)
	return nil
}

func (c *contractChannel) Send(_ context.Context, msg bus.OutboundMessage) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, msg)
	if c.sendErr != nil {
		return nil, c.sendErr
	}
	return []string{"message-id"}, nil
}

func (c *contractChannel) SendMedia(_ context.Context, msg bus.OutboundMediaMessage) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.media = append(c.media, msg)
	if c.mediaSendErr != nil {
		return nil, c.mediaSendErr
	}
	return []string{"media-id"}, nil
}

type channelContractHarness struct {
	t       *testing.T
	cfg     *config.Config
	bus     *bus.MessageBus
	store   media.MediaStore
	manager *Manager
	channel *contractChannel
}

func newChannelContractHarness(t *testing.T) *channelContractHarness {
	t.Helper()
	RegisterFactory(contractChannelType, func(
		channelName, _ string,
		cfg *config.Config,
		msgBus *bus.MessageBus,
	) (Channel, error) {
		bc := cfg.Channels[channelName]
		return &contractChannel{
			BaseChannel: NewBaseChannel(
				channelName,
				contractChannelType,
				msgBus,
				bc.AllowFrom,
				WithReasoningChannelID(bc.ReasoningChannelID),
			),
		}, nil
	})

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Channels = config.ChannelsConfig{
		contractChannelType: {
			Enabled:            true,
			Type:               contractChannelType,
			AllowFrom:          []string{"*"},
			ReasoningChannelID: "reasoning-channel",
			Settings:           config.RawNode(`{}`),
		},
	}
	cfg.Channels[contractChannelType].SetName(contractChannelType)
	msgBus := bus.NewMessageBus()
	store := media.NewFileMediaStore()
	manager, err := NewManager(cfg, msgBus, store)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if err := manager.StartAll(context.Background()); err != nil {
		t.Fatalf("StartAll() error = %v", err)
	}
	channel, ok := manager.GetChannel(contractChannelType)
	if !ok {
		t.Fatal("contract channel was not registered")
	}
	fake, ok := channel.(*contractChannel)
	if !ok {
		t.Fatalf("channel type = %T, want *contractChannel", channel)
	}
	t.Cleanup(func() {
		_ = manager.StopAll(context.Background())
		msgBus.Close()
		store.Stop()
	})
	return &channelContractHarness{
		t: t, cfg: cfg, bus: msgBus, store: store, manager: manager, channel: fake,
	}
}

func (h *channelContractHarness) waitFor(t *testing.T, description string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func TestChannelContractHarnessInbound(t *testing.T) {
	h := newChannelContractHarness(t)
	ctx := context.Background()
	if err := h.channel.HandleInboundContext(
		ctx,
		"chat-1",
		"hello",
		[]string{"media://image-1"},
		bus.InboundContext{MessageID: "message-1", SenderID: "user-1"},
	); err != nil {
		t.Fatalf("HandleInboundContext() error = %v", err)
	}

	select {
	case msg := <-h.bus.InboundChan():
		if msg.Channel != contractChannelType || msg.Content != "hello" || msg.ChatID != "chat-1" {
			t.Fatalf("inbound message = %+v", msg)
		}
		if len(msg.Media) != 1 || msg.Media[0] != "media://image-1" {
			t.Fatalf("inbound media = %#v", msg.Media)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inbound message")
	}
}

func TestChannelContractHarnessOutboundAndMedia(t *testing.T) {
	h := newChannelContractHarness(t)
	ctx := context.Background()
	outbound := bus.OutboundMessage{
		Channel: contractChannelType,
		ChatID:  "chat-1",
		Content: "reply",
		Context: bus.NewOutboundContext(contractChannelType, "chat-1", ""),
	}
	if err := h.manager.SendMessage(ctx, outbound); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	mediaMessage := bus.OutboundMediaMessage{
		Channel: contractChannelType,
		ChatID:  "chat-1",
		Context: bus.NewOutboundContext(contractChannelType, "chat-1", ""),
		Parts: []bus.MediaPart{{
			Type: "image", Ref: "media://image-1", Filename: "image.png", ContentType: "image/png",
		}},
	}
	if err := h.manager.SendMedia(ctx, mediaMessage); err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}

	h.waitFor(t, "outbound message", func() bool {
		h.channel.mu.Lock()
		defer h.channel.mu.Unlock()
		return len(h.channel.sent) == 1 && len(h.channel.media) == 1
	})
	h.channel.mu.Lock()
	defer h.channel.mu.Unlock()
	if h.channel.sent[0].Content != "reply" {
		t.Fatalf("sent content = %q, want reply", h.channel.sent[0].Content)
	}
	if got := h.channel.media[0].Parts[0]; got.Ref != "media://image-1" || got.Filename != "image.png" {
		t.Fatalf("sent media part = %+v", got)
	}
}

func TestChannelContractHarnessErrors(t *testing.T) {
	h := newChannelContractHarness(t)
	h.channel.mu.Lock()
	h.channel.mediaSendErr = ErrSendFailed
	h.channel.mu.Unlock()
	ctx := context.Background()

	if err := h.manager.SendMessage(ctx, bus.OutboundMessage{
		Channel: "missing", ChatID: "chat-1", Content: "reply",
	}); err == nil {
		t.Fatal("SendMessage() for unknown channel error = nil")
	}
	if err := h.manager.SendMedia(ctx, bus.OutboundMediaMessage{
		Channel: "missing", ChatID: "chat-1",
	}); err == nil {
		t.Fatal("SendMedia() for unknown channel error = nil")
	}
	if err := h.manager.SendMedia(ctx, bus.OutboundMediaMessage{
		Channel: contractChannelType,
		ChatID:  "chat-1",
		Context: bus.NewOutboundContext(contractChannelType, "chat-1", ""),
		Parts:   []bus.MediaPart{{Type: "file", Ref: "media://file-1"}},
	}); !errors.Is(err, ErrSendFailed) {
		t.Fatalf("SendMedia() error = %v, want ErrSendFailed", err)
	}
}

func TestChannelContractHarnessReloadAndStop(t *testing.T) {
	h := newChannelContractHarness(t)
	original := h.channel
	h.cfg.Channels[contractChannelType].Enabled = false
	if err := h.manager.Reload(context.Background(), h.cfg); err != nil {
		t.Fatalf("Reload(disable) error = %v", err)
	}
	original.mu.Lock()
	stops := original.stops
	original.mu.Unlock()
	if stops != 1 || original.IsRunning() {
		t.Fatalf("disabled channel stops = %d, running = %v", stops, original.IsRunning())
	}

	h.cfg.Channels[contractChannelType].Enabled = true
	if err := h.manager.Reload(context.Background(), h.cfg); err != nil {
		t.Fatalf("Reload(enable) error = %v", err)
	}
	reloaded, ok := h.manager.GetChannel(contractChannelType)
	if !ok || reloaded == original {
		t.Fatal("reload did not create a fresh channel instance")
	}
	fake := reloaded.(*contractChannel)
	if !fake.IsRunning() {
		t.Fatal("reloaded channel is not running")
	}

	if err := h.manager.StopAll(context.Background()); err != nil {
		t.Fatalf("StopAll() error = %v", err)
	}
	if fake.IsRunning() {
		t.Fatal("channel still running after StopAll")
	}
	if err := h.manager.StopAll(context.Background()); err != nil {
		t.Fatalf("second StopAll() error = %v", err)
	}
}
