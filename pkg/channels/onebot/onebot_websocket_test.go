package onebot

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSendAPIRequest_ResponseTimeoutAndMarshalError(t *testing.T) {
	ch, _ := newOneBotTestChannel(t)
	if _, err := ch.sendAPIRequest("test", nil, time.Second); err == nil ||
		!strings.Contains(err.Error(), "not connected") {
		t.Fatalf("sendAPIRequest() disconnected error = %v", err)
	}

	url, accepted := startOneBotTestWebSocket(t, nil)
	conn := connectOneBotTestWebSocket(t, ch, url, accepted)
	if _, err := ch.sendAPIRequest("bad", func() {}, time.Second); err == nil ||
		!strings.Contains(err.Error(), "marshal") {
		t.Fatalf("sendAPIRequest() marshal error = %v", err)
	}

	go ch.listen()
	type result struct {
		raw json.RawMessage
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		raw, err := ch.sendAPIRequest("get_login_info", nil, time.Second)
		resultCh <- result{raw: raw, err: err}
	}()
	req := readOneBotTestRequest(t, conn)
	if req.Action != "get_login_info" {
		t.Fatalf("API action = %q", req.Action)
	}
	response := json.RawMessage(`{"status":"ok","echo":"` + req.Echo + `","data":{"user_id":"42","nickname":"Bot"}}`)
	if err := conn.WriteMessage(websocket.TextMessage, response); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	got := <-resultCh
	if got.err != nil || string(got.raw) != string(response) {
		t.Fatalf("sendAPIRequest() = %s, %v", got.raw, got.err)
	}

	if _, err := ch.sendAPIRequest("no_response", nil, 10*time.Millisecond); err == nil ||
		!strings.Contains(err.Error(), "timed out") {
		t.Fatalf("sendAPIRequest() timeout error = %v", err)
	}
}

func TestFetchSelfID_ResponseFormats(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response string
		want     int64
	}{
		{name: "wrapped", response: `{"status":"ok","echo":"%s","data":{"user_id":"42"}}`, want: 42},
		{name: "flat", response: `{"status":"ok","echo":"%s","user_id":43}`, want: 43},
		{name: "invalid", response: `{"status":"ok","echo":"%s","data":{"user_id":"bad"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch, _ := newOneBotTestChannel(t)
			url, accepted := startOneBotTestWebSocket(t, nil)
			conn := connectOneBotTestWebSocket(t, ch, url, accepted)
			go ch.listen()
			done := make(chan struct{})
			go func() {
				ch.fetchSelfID()
				close(done)
			}()
			req := readOneBotTestRequest(t, conn)
			response := strings.ReplaceAll(tc.response, "%s", req.Echo)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(response)); err != nil {
				t.Fatalf("WriteMessage() error = %v", err)
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("timeout waiting for fetchSelfID")
			}
			if got := atomic.LoadInt64(&ch.selfID); got != tc.want {
				t.Fatalf("selfID = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestListen_SkipsMalformedAndAPIResponsesThenHandlesEvent(t *testing.T) {
	ch, messageBus := newOneBotTestChannel(t)
	url, accepted := startOneBotTestWebSocket(t, nil)
	conn := connectOneBotTestWebSocket(t, ch, url, accepted)
	go ch.listen()

	for _, payload := range []string{
		`{`,
		`{"status":"ok"}`,
		`{"echo":"missing","status":"failed"}`,
		`{"post_type":"message","message_type":"private","message_id":1,"user_id":42,"message":"hello"}`,
	} {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
			t.Fatalf("WriteMessage() error = %v", err)
		}
	}
	inbound := waitOneBotTestInbound(t, messageBus)
	if inbound.Content != "hello" {
		t.Fatalf("inbound content = %q", inbound.Content)
	}
	assertNoOneBotTestInbound(t, messageBus)

	_ = conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ch.mu.Lock()
		connected := ch.conn != nil
		ch.mu.Unlock()
		if !connected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("listener did not clear the closed connection")
}
