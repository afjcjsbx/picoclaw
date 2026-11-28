package onebot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

func newOneBotTestChannel(t *testing.T, allowFrom ...string) (*OneBotChannel, *bus.MessageBus) {
	t.Helper()
	messageBus := bus.NewMessageBus()
	ch, err := NewOneBotChannel(
		&config.Channel{AllowFrom: config.FlexibleStringSlice(allowFrom)},
		&config.OneBotSettings{},
		messageBus,
	)
	if err != nil {
		t.Fatalf("NewOneBotChannel() error = %v", err)
	}
	ch.ctx = context.Background()
	return ch, messageBus
}

func startOneBotTestWebSocket(t *testing.T, handle func(*websocket.Conn)) (string, <-chan *websocket.Conn) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	accepted := make(chan *websocket.Conn, 8)
	var mu sync.Mutex
	var conns []*websocket.Conn
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		conns = append(conns, conn)
		mu.Unlock()
		accepted <- conn
		if handle != nil {
			handle(conn)
		}
	}))
	t.Cleanup(func() {
		mu.Lock()
		for _, conn := range conns {
			_ = conn.Close()
		}
		mu.Unlock()
		server.Close()
	})
	return "ws" + strings.TrimPrefix(server.URL, "http"), accepted
}

func disableOneBotTestProxy(t *testing.T) {
	t.Helper()
	oldProxy := websocket.DefaultDialer.Proxy
	websocket.DefaultDialer.Proxy = nil
	t.Cleanup(func() { websocket.DefaultDialer.Proxy = oldProxy })
}

func connectOneBotTestWebSocket(
	t *testing.T,
	ch *OneBotChannel,
	url string,
	accepted <-chan *websocket.Conn,
) *websocket.Conn {
	t.Helper()
	disableOneBotTestProxy(t)
	ch.config.WSUrl = url
	ch.ctx, ch.cancel = context.WithCancel(context.Background())
	t.Cleanup(func() { _ = ch.Stop(context.Background()) })
	if err := ch.connect(); err != nil {
		t.Fatalf("connect() error = %v", err)
	}
	select {
	case conn := <-accepted:
		return conn
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for WebSocket connection")
		return nil
	}
}

type oneBotTestRequest struct {
	Action string `json:"action"`
	Params struct {
		UserID  int64                  `json:"user_id"`
		GroupID int64                  `json:"group_id"`
		Message []oneBotMessageSegment `json:"message"`
	} `json:"params"`
	Echo string `json:"echo"`
}

func readOneBotTestRequest(t *testing.T, conn *websocket.Conn) oneBotTestRequest {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	var req oneBotTestRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("json.Unmarshal(request) error = %v", err)
	}
	return req
}

func waitOneBotTestInbound(t *testing.T, messageBus *bus.MessageBus) bus.InboundMessage {
	t.Helper()
	select {
	case inbound := <-messageBus.InboundChan():
		return inbound
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for inbound message")
		return bus.InboundMessage{}
	}
}

func assertNoOneBotTestInbound(t *testing.T, messageBus *bus.MessageBus) {
	t.Helper()
	select {
	case inbound := <-messageBus.InboundChan():
		t.Fatalf("unexpected inbound message: %+v", inbound)
	default:
	}
}
