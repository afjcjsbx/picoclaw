package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestPeerRequestResponse(t *testing.T) {
	client, server := newPeerPair(t)
	server.Register("echo", func(_ context.Context, params json.RawMessage) (any, error) {
		var request struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, err
		}
		return map[string]string{"text": request.Text}, nil
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var response struct {
		Text string `json:"text"`
	}
	if err := client.Call(ctx, "echo", map[string]string{"text": "hello"}, &response); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if response.Text != "hello" {
		t.Fatalf("response.Text = %q, want hello", response.Text)
	}
}

func TestPeerNotification(t *testing.T) {
	client, server := newPeerPair(t)
	received := make(chan string, 1)
	server.OnNotification("session/cancel", func(_ context.Context, params json.RawMessage) {
		var request CancelNotification
		if err := json.Unmarshal(params, &request); err != nil {
			received <- "invalid"
			return
		}
		received <- request.SessionID
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := client.Notify("session/cancel", CancelNotification{SessionID: "session-1"}); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	select {
	case sessionID := <-received:
		if sessionID != "session-1" {
			t.Fatalf("session ID = %q, want session-1", sessionID)
		}
	case <-ctx.Done():
		t.Fatal("notification was not delivered")
	}
}

func TestPeerAgentToClientRequest(t *testing.T) {
	client, server := newPeerPair(t)
	client.Register("session/request_permission", func(_ context.Context, params json.RawMessage) (any, error) {
		var request RequestPermissionRequest
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, err
		}
		return RequestPermissionResponse{Outcome: PermissionOutcome{Outcome: "selected", OptionID: "allow-once"}}, nil
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var response RequestPermissionResponse
	err := server.Call(ctx, "session/request_permission", RequestPermissionRequest{
		SessionID: "session-1",
		ToolCall: ToolCallUpdate{
			ToolCallID: "call-1",
			Title:      "Run command",
		},
		Options: []PermissionOption{{OptionID: "allow-once", Name: "Allow once", Kind: "allow_once"}},
	}, &response)
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if response.Outcome.OptionID != "allow-once" {
		t.Fatalf("option ID = %q, want allow-once", response.Outcome.OptionID)
	}
}

func TestPeerMethodNotFound(t *testing.T) {
	client, _ := newPeerPair(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	err := client.Call(ctx, "missing", map[string]any{}, nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("Call() error = %v, want RPCError", err)
	}
	if rpcErr.Code != CodeMethodNotFound {
		t.Fatalf("error code = %d, want %d", rpcErr.Code, CodeMethodNotFound)
	}
}

func TestPeerCloseReleasesPendingCall(t *testing.T) {
	client, server := newPeerPair(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	server.Register("never-responds", func(_ context.Context, _ json.RawMessage) (any, error) {
		close(entered)
		<-release
		return nil, nil
	})
	result := make(chan error, 1)
	go func() {
		result <- client.Call(t.Context(), "never-responds", map[string]any{}, nil)
	}()
	<-entered
	server.Close()
	close(release)

	select {
	case err := <-result:
		if !errors.Is(err, ErrPeerClosed) {
			t.Fatalf("Call() error = %v, want ErrPeerClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending Call was not released")
	}
}

func TestPeerMalformedLineReturnsParseError(t *testing.T) {
	var output bytes.Buffer
	peer := NewPeer(strings.NewReader("not-json\n"), &output, defaultMaxMessageBytes)
	if err := peer.Serve(t.Context()); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if !strings.Contains(output.String(), `"code":-32700`) {
		t.Fatalf("output = %q, want parse error", output.String())
	}
}

func TestPeerRejectsOversizedLine(t *testing.T) {
	peer := NewPeer(strings.NewReader(strings.Repeat("x", 64)), &bytes.Buffer{}, 32)
	err := peer.Serve(t.Context())
	if err == nil {
		t.Fatal("Serve() error = nil, want oversized-line error")
	}
}

func TestPeerRejectsBatchAsInvalidRequest(t *testing.T) {
	var output bytes.Buffer
	peer := NewPeer(strings.NewReader("[]\n"), &output, defaultMaxMessageBytes)
	if err := peer.Serve(t.Context()); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if !strings.Contains(output.String(), `"code":-32600`) {
		t.Fatalf("output = %q, want invalid-request error", output.String())
	}
}

func TestRequestIDRejectsFractionalNumber(t *testing.T) {
	if _, ok := requestIDKey(json.RawMessage("1.5")); ok {
		t.Fatal("fractional request ID was accepted")
	}
}

func newPeerPair(t *testing.T) (*Peer, *Peer) {
	t.Helper()
	left, right := net.Pipe()
	client := NewPeer(left, left, defaultMaxMessageBytes)
	server := NewPeer(right, right, defaultMaxMessageBytes)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancel()
		client.Close()
		server.Close()
	})
	go func() {
		_ = server.Serve(ctx)
	}()
	go func() {
		_ = client.Serve(ctx)
	}()
	return client, server
}
