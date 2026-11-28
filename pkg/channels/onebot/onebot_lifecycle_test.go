package onebot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestStartAndReconnect_LocalWebSocket(t *testing.T) {
	disableOneBotTestProxy(t)
	var ready atomic.Bool
	accepted := make(chan *websocket.Conn, 1)
	requestSeen := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			http.Error(w, "bad authorization", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		accepted <- conn
		var req oneBotTestRequest
		if err := conn.ReadJSON(&req); err != nil {
			return
		}
		requestSeen <- struct{}{}
		_ = conn.WriteJSON(map[string]any{"status": "ok", "echo": req.Echo, "data": map[string]any{"user_id": 42}})
	}))
	defer server.Close()

	ch, _ := newOneBotTestChannel(t)
	ch.config.WSUrl = "ws" + strings.TrimPrefix(server.URL, "http")
	ch.config.AccessToken = *config.NewSecureString("secret")
	ch.config.ReconnectInterval = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = ch.Stop(context.Background()) })
	if !ch.IsRunning() {
		t.Fatal("channel should be running after Start")
	}

	ready.Store(true)
	select {
	case <-accepted:
	case <-time.After(8 * time.Second):
		t.Fatal("timeout waiting for reconnect")
	}
	select {
	case <-requestSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for get_login_info after reconnect")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&ch.selfID) == 42 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("self ID was not fetched after reconnect")
}

func TestOneBotLifecycleAndCapabilities(t *testing.T) {
	disableOneBotTestProxy(t)
	ch, _ := newOneBotTestChannel(t)
	if err := ch.Start(context.Background()); err == nil {
		t.Fatal("Start() should reject an empty ws_url")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	ch.config.WSUrl = "ws" + strings.TrimPrefix(server.URL, "http")
	if err := ch.Start(context.Background()); err == nil {
		t.Fatal("Start() should fail when reconnect is disabled")
	}
	if ch.IsRunning() {
		t.Fatal("channel should not be running after failed Start")
	}

	undo, err := ch.ReactToMessage(context.Background(), "private:42", "m1")
	if err != nil || undo == nil {
		t.Fatalf("ReactToMessage(private) returned nil undo=%v, err=%v", undo == nil, err)
	}
	undo()
	if got := ch.VoiceCapabilities(); !got.ASR || !got.TTS {
		t.Fatalf("VoiceCapabilities() = %+v", got)
	}
	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}
