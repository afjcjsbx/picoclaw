//go:build integration

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/netbind"
	"github.com/sipeed/picoclaw/pkg/providers"
)

type gatewayStackProviderRecorder struct {
	mu       sync.Mutex
	requests []string
}

func (r *gatewayStackProviderRecorder) add(content string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, content)
}

func (r *gatewayStackProviderRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...)
}

func newGatewayStackProvider(t *testing.T, recorder *gatewayStackProviderRecorder) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if request.Model != "integration-model" {
			http.Error(w, "unexpected model", http.StatusBadRequest)
			return
		}
		for _, message := range request.Messages {
			if message.Role == "user" {
				recorder.add(message.Content)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		response := `{"choices":[{"message":{"role":"assistant","content":"integration answer"},` +
			`"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`
		_, _ = fmt.Fprint(w, response)
	}))
	t.Cleanup(server.Close)
	return server
}

type gatewayStackOneBotRequest struct {
	Action string         `json:"action"`
	Params map[string]any `json:"params"`
	Echo   string         `json:"echo"`
}

func newGatewayStackOneBot(t *testing.T) (*httptest.Server, <-chan string) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	outbound := make(chan string, 4)
	var connsMu sync.Mutex
	var conns []*websocket.Conn
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connsMu.Lock()
		conns = append(conns, conn)
		connsMu.Unlock()
		go func() {
			defer conn.Close()
			sentInbound := false
			for {
				var request gatewayStackOneBotRequest
				if err := conn.ReadJSON(&request); err != nil {
					return
				}
				var data any
				switch request.Action {
				case "get_login_info":
					data = map[string]any{"user_id": 7, "nickname": "Pico"}
				case "send_private_msg":
					if text := gatewayStackOneBotText(request.Params["message"]); text != "" {
						outbound <- text
					}
					data = map[string]any{"message_id": 99}
				default:
					data = map[string]any{}
				}
				if err := conn.WriteJSON(map[string]any{
					"status": "ok", "retcode": 0, "data": data, "echo": request.Echo,
				}); err != nil {
					return
				}
				if !sentInbound {
					sentInbound = true
					if err := conn.WriteJSON(map[string]any{
						"post_type":    "message",
						"message_type": "private",
						"user_id":      42,
						"self_id":      7,
						"message_id":   1,
						"message":      "hello integration",
						"sender":       map[string]any{"user_id": 42, "nickname": "Ada"},
					}); err != nil {
						return
					}
				}
			}
		}()
	}))
	t.Cleanup(func() {
		connsMu.Lock()
		for _, conn := range conns {
			_ = conn.Close()
		}
		connsMu.Unlock()
		server.Close()
	})
	return server, outbound
}

func gatewayStackOneBotText(raw any) string {
	switch value := raw.(type) {
	case string:
		return value
	case []any:
		var parts []string
		for _, item := range value {
			segment, ok := item.(map[string]any)
			if !ok || segment["type"] != "text" {
				continue
			}
			data, ok := segment["data"].(map[string]any)
			if !ok {
				continue
			}
			if text, ok := data["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func TestIntegration_GatewayStack(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	recorder := &gatewayStackProviderRecorder{}
	providerServer := newGatewayStackProvider(t, recorder)
	oneBotServer, outbound := newGatewayStackOneBot(t)
	wsURL := "ws" + strings.TrimPrefix(oneBotServer.URL, "http")

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.ModelName = "integration-model"
	cfg.Agents.Defaults.MaxToolIterations = 1
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "integration-model",
		Model:     "openai/integration-model",
		APIBase:   providerServer.URL,
	}}
	cfg.ModelList[0].SetAPIKey("integration-key")
	cfg.Channels = config.ChannelsConfig{
		config.ChannelOneBot: {
			Enabled:   true,
			Type:      config.ChannelOneBot,
			AllowFrom: []string{"42"},
			Settings:  config.RawNode(fmt.Sprintf(`{"ws_url":%q,"reconnect_interval":0}`, wsURL)),
		},
	}
	cfg.Channels[config.ChannelOneBot].SetName(config.ChannelOneBot)
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Port = 0
	cfg.Gateway.HotReload = false
	cfg.Heartbeat.Enabled = false
	cfg.Devices.Enabled = false
	cfg.Evolution.Enabled = false
	cfg.Tools.MediaCleanup.Enabled = false

	provider, modelID, err := createStartupProvider(cfg, false)
	if err != nil {
		t.Fatalf("createStartupProvider() error = %v", err)
	}
	cfg.Agents.Defaults.ModelName = providers.DefaultModelRef(cfg, modelID)
	msgBus := bus.NewMessageBus()
	al := agent.NewAgentLoop(cfg, msgBus, provider)
	runningServices, err := setupAndStartServices(
		cfg,
		al,
		msgBus,
		"integration-token",
		netbind.OpenResult{ProbeHost: "127.0.0.1"},
	)
	if err != nil {
		t.Fatalf("setupAndStartServices() error = %v", err)
	}
	runningServices.HealthServer.SetReady(true)

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		al.Stop()
		select {
		case err := <-runDone:
			if err != nil {
				t.Errorf("AgentLoop.Run() error = %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("timed out waiting for AgentLoop.Run() to stop")
		}
		stopAndCleanupServices(runningServices, 3*time.Second, false)
		al.Close()
		msgBus.Close()
	})

	select {
	case answer := <-outbound:
		if answer != "integration answer" {
			t.Fatalf("OneBot outbound = %q, want integration answer", answer)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for OneBot outbound response")
	}

	requests := recorder.snapshot()
	if len(requests) != 1 || requests[0] != "hello integration" {
		t.Fatalf("provider user requests = %#v, want [hello integration]", requests)
	}
	if got := runningServices.ChannelManager.GetEnabledChannels(); len(got) != 1 || got[0] != config.ChannelOneBot {
		t.Fatalf("enabled channels = %#v, want [onebot]", got)
	}
}
