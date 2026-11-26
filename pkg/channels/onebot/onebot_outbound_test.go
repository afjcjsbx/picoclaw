package onebot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/media"
)

func TestOneBotBuildSendRequest(t *testing.T) {
	ch := &OneBotChannel{}
	ch.lastMessageID.Store("group:42", "m1")

	for _, tc := range []struct {
		chatID string
		action string
		idKey  string
		id     int64
	}{
		{chatID: "group:42", action: "send_group_msg", idKey: "group_id", id: 42},
		{chatID: "private:7", action: "send_private_msg", idKey: "user_id", id: 7},
		{chatID: "7", action: "send_private_msg", idKey: "user_id", id: 7},
	} {
		t.Run(tc.chatID, func(t *testing.T) {
			action, params, err := ch.buildSendRequest(bus.OutboundMessage{ChatID: tc.chatID, Content: "hello"})
			if err != nil {
				t.Fatalf("buildSendRequest() error = %v", err)
			}
			if action != tc.action {
				t.Fatalf("action = %q, want %q", action, tc.action)
			}
			got := params.(map[string]any)
			if got[tc.idKey] != tc.id {
				t.Fatalf("%s = %v, want %d", tc.idKey, got[tc.idKey], tc.id)
			}
			segments := got["message"].([]oneBotMessageSegment)
			wantLen := 1
			if tc.chatID == "group:42" {
				wantLen = 2
				if segments[0].Type != "reply" || segments[0].Data["id"] != "m1" {
					t.Fatalf("reply segment = %+v", segments[0])
				}
			}
			if len(segments) != wantLen || segments[wantLen-1].Data["text"] != "hello" {
				t.Fatalf("message segments = %+v", segments)
			}
		})
	}

	if _, _, err := ch.buildSendRequest(bus.OutboundMessage{ChatID: "group:nope"}); err == nil {
		t.Fatal("buildSendRequest() should reject an invalid chat ID")
	}
}

func TestSendAndSendMedia_WriteExpectedRequests(t *testing.T) {
	ch, _ := newOneBotTestChannel(t)
	url, accepted := startOneBotTestWebSocket(t, nil)
	conn := connectOneBotTestWebSocket(t, ch, url, accepted)
	ch.SetRunning(true)
	ch.lastMessageID.Store("group:42", "m1")

	if _, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "group:42", Content: "hello"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	req := readOneBotTestRequest(t, conn)
	if req.Action != "send_group_msg" || req.Params.GroupID != 42 || req.Echo == "" {
		t.Fatalf("Send request = %+v", req)
	}
	if len(req.Params.Message) != 2 || req.Params.Message[0].Type != "reply" || req.Params.Message[1].Type != "text" {
		t.Fatalf("Send message = %+v", req.Params.Message)
	}

	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)
	tmpDir := t.TempDir()
	parts := []bus.MediaPart{
		{Type: "image", Caption: "picture", Filename: "picture.png"},
		{Type: "video", Filename: "clip.mp4"},
		{Type: "audio", Filename: "voice.amr"},
		{Type: "file", Filename: "document.pdf"},
	}
	for i, part := range parts {
		path := filepath.Join(tmpDir, part.Filename)
		if err := os.WriteFile(path, []byte(part.Filename), 0o600); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
		ref, err := store.Store(path, media.MediaMeta{Filename: part.Filename}, "scope")
		if err != nil {
			t.Fatalf("Store() error = %v", err)
		}
		parts[i].Ref = ref
	}
	if _, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "private:42", Parts: parts,
	}); err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}
	req = readOneBotTestRequest(t, conn)
	if req.Action != "send_private_msg" || req.Params.UserID != 42 {
		t.Fatalf("SendMedia request = %+v", req)
	}
	wantTypes := []string{"image", "text", "video", "record", "file"}
	if len(req.Params.Message) != len(wantTypes) {
		t.Fatalf("SendMedia segments = %+v", req.Params.Message)
	}
	for i, want := range wantTypes {
		if req.Params.Message[i].Type != want {
			t.Errorf("segment %d type = %q, want %q", i, req.Params.Message[i].Type, want)
		}
	}
	if got := req.Params.Message[0].Data["file"]; got != "file://"+filepath.Join(tmpDir, "picture.png") {
		t.Errorf("image file = %v", got)
	}
	if got := req.Params.Message[1].Data["text"]; got != "picture" {
		t.Errorf("caption = %v", got)
	}
}

func TestSendErrorsAndEmptyMedia(t *testing.T) {
	ch, _ := newOneBotTestChannel(t)
	if _, err := ch.Send(context.Background(), bus.OutboundMessage{}); !errors.Is(err, channels.ErrNotRunning) {
		t.Fatalf("Send() error = %v, want ErrNotRunning", err)
	}
	if _, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{}); !errors.Is(
		err,
		channels.ErrNotRunning,
	) {
		t.Fatalf("SendMedia() error = %v, want ErrNotRunning", err)
	}

	ch.SetRunning(true)
	if _, err := ch.Send(context.Background(), bus.OutboundMessage{}); err == nil ||
		!strings.Contains(err.Error(), "not connected") {
		t.Fatalf("Send() disconnected error = %v", err)
	}
	if _, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{}); err == nil ||
		!strings.Contains(err.Error(), "not connected") {
		t.Fatalf("SendMedia() disconnected error = %v", err)
	}

	url, accepted := startOneBotTestWebSocket(t, nil)
	_ = connectOneBotTestWebSocket(t, ch, url, accepted)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ch.Send(ctx, bus.OutboundMessage{ChatID: "42"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Send() canceled error = %v", err)
	}
	if _, err := ch.SendMedia(ctx, bus.OutboundMediaMessage{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("SendMedia() canceled error = %v", err)
	}
	if _, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "bad"}); err == nil {
		t.Fatal("Send() should reject invalid chat ID")
	}
	if _, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{ChatID: "bad"}); !errors.Is(
		err,
		channels.ErrSendFailed,
	) {
		t.Fatalf("SendMedia() without store error = %v, want ErrSendFailed", err)
	}

	ch.SetMediaStore(media.NewFileMediaStore())
	if _, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "42",
		Parts:  []bus.MediaPart{{Type: "image", Ref: "media://missing"}},
	}); err != nil {
		t.Fatalf("SendMedia() unknown ref error = %v", err)
	}
	if _, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "bad",
		Parts:  []bus.MediaPart{{Ref: "media://missing"}},
	}); err != nil {
		t.Fatalf("SendMedia() empty segments error = %v", err)
	}

	ch.mu.Lock()
	_ = ch.conn.Close()
	ch.mu.Unlock()
	_, err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID: "42", Content: "hello",
	})
	if !errors.Is(err, channels.ErrTemporary) {
		t.Fatalf("Send() closed connection error = %v, want ErrTemporary", err)
	}
}
