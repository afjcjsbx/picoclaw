package maixcam

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
)

func newTestMaixCamChannel(t *testing.T, allowFrom ...string) (*MaixCamChannel, *bus.MessageBus) {
	t.Helper()

	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	ch, err := NewMaixCamChannel(
		&config.Channel{
			AllowFrom:          config.FlexibleStringSlice(allowFrom),
			ReasoningChannelID: "reasoning",
		},
		&config.MaixCamSettings{Host: "127.0.0.1", Port: 0},
		msgBus,
	)
	if err != nil {
		t.Fatalf("NewMaixCamChannel() error = %v", err)
	}
	return ch, msgBus
}

func receiveMaixCamInbound(t *testing.T, msgBus *bus.MessageBus) bus.InboundMessage {
	t.Helper()

	select {
	case msg := <-msgBus.InboundChan():
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for inbound MaixCam message")
		return bus.InboundMessage{}
	}
}

func assertNoMaixCamInbound(t *testing.T, msgBus *bus.MessageBus) {
	t.Helper()

	select {
	case msg := <-msgBus.InboundChan():
		t.Fatalf("unexpected inbound MaixCam message: %+v", msg)
	default:
	}
}

func startMaixCamTestConnection(t *testing.T, ch *MaixCamChannel) (net.Conn, <-chan struct{}) {
	t.Helper()

	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ch.handleConnection(server)
	}()
	t.Cleanup(func() {
		_ = client.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("timed out waiting for MaixCam connection handler to stop")
		}
	})
	return client, done
}

func waitForMaixCamClients(t *testing.T, ch *MaixCamChannel, want int) {
	t.Helper()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()

	for {
		ch.clientsMux.RLock()
		got := len(ch.clients)
		ch.clientsMux.RUnlock()
		if got == want {
			return
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatalf("connected client count = %d, want %d", got, want)
		}
	}
}

func TestNewMaixCamChannel(t *testing.T) {
	cfg := &config.MaixCamSettings{}
	bc := &config.Channel{ReasoningChannelID: "reasoning"}
	ch, err := NewMaixCamChannel(bc, cfg, bus.NewMessageBus())
	if err != nil {
		t.Fatalf("NewMaixCamChannel() error = %v", err)
	}
	if ch.Name() != "maixcam" {
		t.Errorf("Name() = %q, want maixcam", ch.Name())
	}
	if ch.config != cfg {
		t.Error("channel does not retain the supplied settings")
	}
	if ch.ReasoningChannelID() != bc.ReasoningChannelID {
		t.Errorf("ReasoningChannelID() = %q, want %q", ch.ReasoningChannelID(), bc.ReasoningChannelID)
	}
	if ch.IsRunning() {
		t.Error("new channel is running")
	}
	if ch.clients == nil || len(ch.clients) != 0 {
		t.Errorf("clients = %v, want initialized empty map", ch.clients)
	}
}

func TestMaixCamHandleConnectionPublishesPersonDetection(t *testing.T) {
	ch, msgBus := newTestMaixCamChannel(t, "maixcam:maixcam")
	ch.ctx = context.Background()
	client, _ := startMaixCamTestConnection(t, ch)

	message := MaixCamMessage{
		Type:      "person_detected",
		Tips:      "front camera",
		Timestamp: 1700000000.25,
		Data: map[string]any{
			"class_name": "person",
			"class_id":   float64(2),
			"score":      0.875,
			"x":          12.4,
			"y":          5.6,
			"w":          30.2,
			"h":          40.8,
		},
	}
	if err := json.NewEncoder(client).Encode(message); err != nil {
		t.Fatalf("encoding inbound message: %v", err)
	}

	msg := receiveMaixCamInbound(t, msgBus)
	wantContent := "📷 Person detected!\nClass: person\nConfidence: 87.50%\nPosition: (12, 6)\nSize: 30x41"
	if msg.Content != wantContent {
		t.Errorf("Content = %q, want %q", msg.Content, wantContent)
	}
	if msg.Channel != "maixcam" || msg.Context.Channel != "maixcam" {
		t.Errorf("Channel = %q, Context.Channel = %q, want maixcam", msg.Channel, msg.Context.Channel)
	}
	if msg.ChatID != "default" || msg.Context.ChatID != "default" {
		t.Errorf("ChatID = %q, Context.ChatID = %q, want default", msg.ChatID, msg.Context.ChatID)
	}
	if msg.Context.ChatType != "channel" {
		t.Errorf("ChatType = %q, want channel", msg.Context.ChatType)
	}
	if msg.SenderID != "maixcam" || msg.Context.SenderID != "maixcam" {
		t.Errorf("SenderID = %q, Context.SenderID = %q, want maixcam", msg.SenderID, msg.Context.SenderID)
	}
	wantSender := bus.SenderInfo{
		Platform:    "maixcam",
		PlatformID:  "maixcam",
		CanonicalID: "maixcam:maixcam",
	}
	if msg.Sender != wantSender {
		t.Errorf("Sender = %+v, want %+v", msg.Sender, wantSender)
	}
	wantRaw := map[string]string{
		"timestamp": "1700000000",
		"class_id":  "2",
		"score":     "0.88",
		"x":         "12",
		"y":         "6",
		"w":         "30",
		"h":         "41",
	}
	if !reflect.DeepEqual(msg.Context.Raw, wantRaw) {
		t.Errorf("Context.Raw = %v, want %v", msg.Context.Raw, wantRaw)
	}
	if len(msg.Media) != 0 {
		t.Errorf("Media = %v, want none", msg.Media)
	}
}

func TestMaixCamHandleConnectionRejectsMalformedJSON(t *testing.T) {
	ch, msgBus := newTestMaixCamChannel(t)
	ch.ctx = context.Background()
	client, done := startMaixCamTestConnection(t, ch)

	if _, err := io.WriteString(client, `{"type":"person_detected","data":`); err != nil {
		t.Fatalf("writing malformed inbound message: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("closing malformed inbound connection: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for malformed connection to close")
	}
	assertNoMaixCamInbound(t, msgBus)
}

func TestMaixCamPersonDetectionAllowlist(t *testing.T) {
	tests := []struct {
		name      string
		allowFrom []string
		want      bool
	}{
		{name: "empty allows all", want: true},
		{name: "platform ID", allowFrom: []string{"maixcam"}, want: true},
		{name: "canonical ID", allowFrom: []string{"maixcam:maixcam"}, want: true},
		{name: "wildcard", allowFrom: []string{"*"}, want: true},
		{name: "different sender denied", allowFrom: []string{"other"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, msgBus := newTestMaixCamChannel(t, tt.allowFrom...)
			ch.ctx = context.Background()
			ch.processMessage(MaixCamMessage{
				Type: "person_detected",
				Data: map[string]any{"class_id": float64(0)},
			}, nil)
			if tt.want {
				receiveMaixCamInbound(t, msgBus)
			} else {
				assertNoMaixCamInbound(t, msgBus)
			}
		})
	}
}

func TestMaixCamPersonDetectionMalformedDataUsesDefaults(t *testing.T) {
	ch, msgBus := newTestMaixCamChannel(t)
	ch.ctx = context.Background()
	ch.processMessage(MaixCamMessage{
		Type: "person_detected",
		Data: map[string]any{
			"class_name": 42,
			"class_id":   float64(0),
			"score":      "high",
			"x":          nil,
			"y":          []any{1},
			"w":          true,
			"h":          map[string]any{},
		},
	}, nil)

	msg := receiveMaixCamInbound(t, msgBus)
	wantContent := "📷 Person detected!\nClass: person\nConfidence: 0.00%\nPosition: (0, 0)\nSize: 0x0"
	if msg.Content != wantContent {
		t.Errorf("Content = %q, want %q", msg.Content, wantContent)
	}
}

func TestMaixCamProcessMessageIgnoresNonDetectionTypes(t *testing.T) {
	for _, messageType := range []string{"heartbeat", "status", "unknown"} {
		t.Run(messageType, func(t *testing.T) {
			ch, msgBus := newTestMaixCamChannel(t)
			ch.ctx = context.Background()
			ch.processMessage(MaixCamMessage{Type: messageType, Data: map[string]any{"status": "ok"}}, nil)
			assertNoMaixCamInbound(t, msgBus)
		})
	}
}

func TestMaixCamChannelStartAndStop(t *testing.T) {
	ch, _ := newTestMaixCamChannel(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t.Cleanup(func() { _ = ch.Stop(context.Background()) })

	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !ch.IsRunning() {
		t.Fatal("channel is not running after Start()")
	}
	if ch.listener == nil {
		t.Fatal("listener is nil after Start()")
	}

	conn, err := net.DialTimeout("tcp", ch.listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dialing local MaixCam listener: %v", err)
	}
	defer conn.Close()
	waitForMaixCamClients(t, ch, 1)

	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if ch.IsRunning() {
		t.Error("channel is still running after Stop()")
	}
	if !errors.Is(ch.ctx.Err(), context.Canceled) {
		t.Errorf("context error = %v, want context.Canceled", ch.ctx.Err())
	}
	ch.clientsMux.RLock()
	clientCount := len(ch.clients)
	ch.clientsMux.RUnlock()
	if clientCount != 0 {
		t.Errorf("clients after Stop() = %d, want 0", clientCount)
	}
	if _, err := ch.listener.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Accept() after Stop() error = %v, want net.ErrClosed", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("setting client read deadline: %v", err)
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Error("client connection remained open after Stop()")
	}
}

func TestMaixCamChannelStartListenError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving local port: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()
	ch, err := NewMaixCamChannel(&config.Channel{}, &config.MaixCamSettings{
		Host: "127.0.0.1",
		Port: port,
	}, msgBus)
	if err != nil {
		t.Fatalf("NewMaixCamChannel() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = ch.Start(ctx)
	if err == nil || !strings.Contains(err.Error(), "failed to listen on 127.0.0.1:") {
		t.Fatalf("Start() error = %v, want local listen failure", err)
	}
	if ch.IsRunning() {
		t.Error("channel is running after failed Start()")
	}
	if ch.cancel == nil || !errors.Is(ch.ctx.Err(), context.Canceled) {
		t.Errorf("failed Start() context error = %v, want context.Canceled", ch.ctx.Err())
	}
}

func TestMaixCamChannelStopBeforeStart(t *testing.T) {
	ch, _ := newTestMaixCamChannel(t)
	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if ch.IsRunning() {
		t.Error("channel is running after Stop() before Start()")
	}
	if ch.clients == nil || len(ch.clients) != 0 {
		t.Errorf("clients after Stop() = %v, want initialized empty map", ch.clients)
	}
}

func TestMaixCamChannelSend(t *testing.T) {
	t.Run("not running", func(t *testing.T) {
		ch, _ := newTestMaixCamChannel(t)
		got, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "default", Content: "hello"})
		if got != nil || !errors.Is(err, channels.ErrNotRunning) {
			t.Fatalf("Send() = (%v, %v), want (nil, ErrNotRunning)", got, err)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		ch, _ := newTestMaixCamChannel(t)
		ch.SetRunning(true)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := ch.Send(ctx, bus.OutboundMessage{ChatID: "default", Content: "hello"})
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("Send() = (%v, %v), want (nil, context.Canceled)", got, err)
		}
	})

	t.Run("no connected devices", func(t *testing.T) {
		ch, _ := newTestMaixCamChannel(t)
		ch.SetRunning(true)
		got, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "default", Content: "hello"})
		if got != nil || err == nil || err.Error() != "no connected MaixCam devices" {
			t.Fatalf("Send() = (%v, %v), want no connected devices error", got, err)
		}
	})

	t.Run("writes command to every client", func(t *testing.T) {
		ch, _ := newTestMaixCamChannel(t)
		ch.SetRunning(true)

		peers := make([]net.Conn, 0, 2)
		servers := make([]net.Conn, 0, 2)
		for range 2 {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			peers = append(peers, client)
			servers = append(servers, server)
			ch.clients[server] = true
		}

		type response struct {
			Type      string  `json:"type"`
			Timestamp float64 `json:"timestamp"`
			Message   string  `json:"message"`
			ChatID    string  `json:"chat_id"`
		}
		type decodeResult struct {
			response response
			err      error
		}
		decoded := make(chan decodeResult, len(peers))
		for _, peer := range peers {
			go func(conn net.Conn) {
				var got response
				err := json.NewDecoder(conn).Decode(&got)
				decoded <- decodeResult{response: got, err: err}
			}(peer)
		}

		sendDone := make(chan error, 1)
		content := "hello \"MaixCam\"\nsecond line"
		go func() {
			_, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "default", Content: content})
			sendDone <- err
		}()

		for range peers {
			select {
			case result := <-decoded:
				if result.err != nil {
					t.Fatalf("decoding outbound command: %v", result.err)
				}
				want := response{Type: "command", Timestamp: 0, Message: content, ChatID: "default"}
				if result.response != want {
					t.Errorf("outbound command = %+v, want %+v", result.response, want)
				}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for outbound MaixCam command")
			}
		}
		select {
		case err := <-sendDone:
			if err != nil {
				t.Fatalf("Send() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for Send() to return")
		}
	})

	t.Run("write error is temporary", func(t *testing.T) {
		ch, _ := newTestMaixCamChannel(t)
		ch.SetRunning(true)
		server, peer := net.Pipe()
		defer server.Close()
		if err := peer.Close(); err != nil {
			t.Fatalf("closing peer connection: %v", err)
		}
		ch.clients[server] = true

		got, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "default", Content: "hello"})
		if got != nil || !errors.Is(err, channels.ErrTemporary) {
			t.Fatalf("Send() = (%v, %v), want (nil, ErrTemporary)", got, err)
		}
		if !strings.Contains(err.Error(), "maixcam send") {
			t.Errorf("Send() error = %v, want maixcam send context", err)
		}
	})
}
