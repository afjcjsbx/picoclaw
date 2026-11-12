package deltachat

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/media"
	fstools "github.com/sipeed/picoclaw/pkg/tools/fs"
)

func TestListenWaitsForCompleteImage(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()
	ch := newTestChannelWithBus(t, msgBus, nil)
	ch.ctx, ch.cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer ch.cancel()
	ch.accountID = 7
	ch.SetRunning(true)
	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)
	filename := "holiday photo – 東京.png"
	path := filepath.Join(t.TempDir(), filename)
	data := []byte("\x89PNG\r\n\x1a\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	complete := false
	seen := map[int64]bool{}
	eventIndex := 0
	rpc, cleanup := newMockRPC(t, func(req rpcRequest) string {
		switch req.Method {
		case "get_next_msgs":
			return rpcResult(req, []int64{42, 43})
		case "get_message":
			id := int64(req.Params[1].(float64))
			msg := map[string]any{"id": id, "chatId": 12, "fromId": 10, "text": "hello", "downloadState": "Done", "state": 10, "sender": map[string]any{"address": "alice@example.org"}}
			if id == 42 {
				msg["text"] = "Image – 304.82 KiB"
				msg["downloadState"] = "Available"
				if complete {
					msg["text"] = "describe this"
					msg["downloadState"] = "Done"
					msg["file"] = path
					msg["fileName"] = filename
					msg["fileMime"] = "image/png"
				}
			}
			if seen[id] {
				msg["state"] = 26
			}
			return rpcResult(req, msg)
		case "get_full_chat_by_id":
			return rpcResult(req, map[string]any{"id": 12, "chatType": "Single", "canSend": true})
		case "markseen_msgs":
			ids := req.Params[1].([]any)
			seen[int64(ids[0].(float64))] = true
			return rpcResult(req, nil)
		case "get_next_event":
			eventIndex++
			accountID, kind, id := int64(7), "IncomingMsg", int64(42)
			switch eventIndex {
			case 1:
				accountID = 8 // Other accounts must not dispatch.
			case 2:
				kind = "MsgsChanged" // Preview/update events must not dispatch.
			case 3:
				if seen[42] {
					t.Error("preview was marked seen before attachment arrived")
				}
				complete = true
			case 4:
				id = 43 // Already dispatched by startup replay.
			case 5: // Duplicate completed event must not dispatch again.
			default:
				ch.cancel()
			}
			return rpcResult(req, map[string]any{"contextId": accountID, "event": map[string]any{"kind": kind, "msgId": id}})
		default:
			t.Errorf("unexpected RPC method %s", req.Method)
			return rpcUnexpectedMethod(req)
		}
	})
	ch.rpc = rpc
	defer cleanup()
	ch.listen()
	if !seen[42] || !seen[43] {
		t.Fatalf("completed messages not marked seen: %v", seen)
	}
	var messages []bus.InboundMessage
	for len(msgBus.InboundChan()) > 0 {
		messages = append(messages, <-msgBus.InboundChan())
	}
	if len(messages) != 2 {
		t.Fatalf("got %d messages, want text and completed image", len(messages))
	}
	msg := messages[1]
	if msg.Content != "describe this" || len(msg.Media) != 1 {
		t.Fatalf("image message = %+v", msg)
	}
	localPath, meta, err := store.ResolveWithMeta(msg.Media[0])
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(localPath)
	if meta.Filename != filename || meta.ContentType != "image/png" {
		t.Fatalf("metadata = %+v", meta)
	}
	copied, err := os.ReadFile(localPath)
	if err != nil || !bytes.Equal(copied, data) {
		t.Fatalf("copied image = %q, err = %v", copied, err)
	}
	// Exercise the actual load_image tool with workspace restrictions enabled:
	// spaces and Unicode in the attachment name must not prevent loading it.
	// The agent supplies this media-directory read allowance to its tools.
	allow := regexp.MustCompile("^" + regexp.QuoteMeta(media.TempDir()+string(os.PathSeparator)))
	tool := fstools.NewLoadImageTool(t.TempDir(), true, 0, store, []*regexp.Regexp{allow})
	ctx := fstools.WithToolContext(context.Background(), "deltachat", "12")
	result := tool.Execute(ctx, map[string]any{"path": localPath})
	if result.IsError || len(result.Media) != 1 {
		t.Fatalf("load_image = %+v", result)
	}
}

func TestHandleMessageSkipsIncompleteDownloads(t *testing.T) {
	for _, state := range []string{"Available", "InProgress", "Failure", "Undecipherable"} {
		t.Run(state, func(t *testing.T) {
			msgBus := bus.NewMessageBus()
			defer msgBus.Close()
			ch := newTestChannelWithBus(t, msgBus, nil)
			ch.ctx = context.Background()
			rpc, cleanup := newMockRPC(t, func(req rpcRequest) string {
				if req.Method != "get_message" {
					t.Errorf("unexpected RPC %s", req.Method)
					return rpcUnexpectedMethod(req)
				}
				return rpcResult(req, map[string]any{"id": 42, "chatId": 12, "text": "Image – 304.82 KiB", "downloadState": state})
			})
			defer cleanup()
			ch.rpc = rpc
			ch.handleMessage(42)
			if len(msgBus.InboundChan()) != 0 {
				t.Fatal("incomplete message dispatched")
			}
		})
	}
}
