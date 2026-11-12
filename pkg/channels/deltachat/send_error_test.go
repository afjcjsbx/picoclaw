package deltachat

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

func TestSendErrorsPreserveCauseAndRetryPolicy(t *testing.T) {
	for _, useMedia := range []bool{false, true} {
		for _, tc := range []struct {
			name, message string
			kind          error
		}{
			{"device", "Cannot send to Chat#11: the chat is a device chat", channels.ErrSendFailed},
			{"temporary", "database is locked", channels.ErrTemporary},
		} {
			name := "text/" + tc.name
			if useMedia {
				name = "media/" + tc.name
			}
			t.Run(name, func(t *testing.T) {
				ch := newTestChannel(t)
				ch.SetRunning(true)
				ch.accountID = 7
				rpc, cleanup := newMockRPC(t, func(req rpcRequest) string {
					want := "misc_send_msg"
					if useMedia {
						want = "send_msg"
					}
					if req.Method != want {
						t.Errorf("RPC method = %s, want %s", req.Method, want)
					}
					return `{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"error":{"code":-1,"message":"` + tc.message + `"}}`
				})
				defer cleanup()
				ch.rpc = rpc
				var err error
				if useMedia {
					path := filepath.Join(t.TempDir(), "photo.png")
					if err := os.WriteFile(path, []byte("image"), 0o600); err != nil {
						t.Fatal(err)
					}
					store := media.NewFileMediaStore()
					ch.SetMediaStore(store)
					ref, storeErr := store.Store(path, media.MediaMeta{}, "test")
					if storeErr != nil {
						t.Fatal(storeErr)
					}
					_, err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{ChatID: "11", Parts: []bus.MediaPart{{Ref: ref, Type: "image"}}})
				} else {
					_, err = ch.Send(context.Background(), bus.OutboundMessage{ChatID: "11", Content: "hello"})
				}
				if !errors.Is(err, tc.kind) {
					t.Fatalf("error = %v, want %v", err, tc.kind)
				}
				var cause *rpcError
				if !errors.As(err, &cause) || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("RPC cause lost: %v", err)
				}
			})
		}
	}
}

func TestHandleMessageIgnoresDeviceChat(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()
	ch := newTestChannelWithBus(t, msgBus, nil)
	ch.ctx = context.Background()
	rpc, cleanup := newMockRPC(t, func(req rpcRequest) string {
		switch req.Method {
		case "get_message":
			return rpcResult(req, map[string]any{"id": 42, "chatId": 11, "text": "Welcome to Delta Chat", "downloadState": "Done"})
		case "get_full_chat_by_id":
			return rpcResult(req, map[string]any{"id": 11, "chatType": "Single", "isDeviceChat": true, "canSend": false})
		default:
			t.Errorf("unexpected RPC %s", req.Method)
			return rpcUnexpectedMethod(req)
		}
	})
	defer cleanup()
	ch.rpc = rpc
	ch.handleMessage(42)
	if len(msgBus.InboundChan()) != 0 {
		t.Fatal("device message dispatched to agent")
	}
}
