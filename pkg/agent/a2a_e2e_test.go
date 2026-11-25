package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	proto "github.com/sipeed/picoclaw/pkg/a2a"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	a2achannel "github.com/sipeed/picoclaw/pkg/channels/a2a"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// a2aProvider answers "turn=<n> msgs=<len>" so a test can see how much history
// each call carried; a request containing "block" waits for its context.
type a2aProvider struct {
	mu       sync.Mutex
	calls    int
	canceled chan struct{}
}

func (p *a2aProvider) Chat(
	ctx context.Context,
	msgs []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	if last := msgs[len(msgs)-1].Content; strings.Contains(last, "block") {
		<-ctx.Done()
		close(p.canceled)
		return nil, ctx.Err()
	}
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.mu.Unlock()
	return &providers.LLMResponse{Content: fmt.Sprintf("call=%d msgs=%d", n, len(msgs))}, nil
}

func (p *a2aProvider) GetDefaultModel() string { return "mock" }

func a2aRPC(t *testing.T, url, method string, params any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, _ := http.NewRequest(http.MethodPost, url+"/a2a", strings.NewReader(string(body)))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func a2aMsg(id, ctx, text string) map[string]any {
	return map[string]any{"message": map[string]any{
		"messageId": id, "contextId": ctx, "role": proto.RoleUser, "parts": []any{map[string]any{"text": text}},
	}}
}

func a2aResult(t *testing.T, out map[string]any) (state, text string) {
	t.Helper()
	res, ok := out["result"].(map[string]any)
	require.True(t, ok, "rpc failed: %v", out)
	if tk, ok := res["task"].(map[string]any); ok {
		res = tk
	}
	state = res["status"].(map[string]any)["state"].(string)
	if arts, ok := res["artifacts"].([]any); ok {
		text = arts[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
	}
	return state, text
}

// TestA2AChannelThroughRealAgentLoop drives the real Manager + AgentLoop with a
// fake model: reply routing, per-context sessions, and a CancelTask that must
// actually interrupt the running turn.
func TestA2AChannelThroughRealAgentLoop(t *testing.T) {
	cfg := config.DefaultConfig() // default session.dimensions = ["chat"]
	// Not t.TempDir(): the canceled turn rolls its session back on disk after
	// the test body ends, and TempDir cleanup would race with it (same reason
	// newTestAgentLoop uses MkdirTemp).
	ws, err := os.MkdirTemp("", "a2a-e2e-*")
	require.NoError(t, err)
	defer os.RemoveAll(ws)
	cfg.Agents.Defaults.Workspace = ws
	msgBus := bus.NewMessageBus()
	prov := &a2aProvider{canceled: make(chan struct{})}
	al := NewAgentLoop(cfg, msgBus, prov)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	ch, err := a2achannel.New(&config.Channel{}, &config.A2ASettings{Port: port}, msgBus)
	require.NoError(t, err)
	cm, err := channels.NewManager(&config.Config{}, msgBus, nil)
	require.NoError(t, err)
	cm.RegisterChannel("a2a", ch)
	al.SetChannelManager(cm)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, cm.StartAll(ctx))
	defer func() { _ = cm.StopAll(context.Background()) }()
	go func() { _ = al.Run(ctx) }()
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Reply is routed back to the right task.
	state, text := a2aResult(t, a2aRPC(t, url, "SendMessage", a2aMsg("m1", "ctx-a", "hello")))
	assert.Equal(t, proto.StateCompleted, state)
	assert.Contains(t, text, "call=1")
	first := text

	// Same context keeps its history; a different context starts clean.
	_, again := a2aResult(t, a2aRPC(t, url, "SendMessage", a2aMsg("m2", "ctx-a", "hello again")))
	_, other := a2aResult(t, a2aRPC(t, url, "SendMessage", a2aMsg("m3", "ctx-b", "hello")))
	msgs := func(s string) string { return s[strings.Index(s, "msgs="):] }
	assert.NotEqual(t, msgs(first), msgs(again), "context a must accumulate history: %q vs %q", first, again)
	assert.Equal(t, msgs(first), msgs(other), "context b must not see context a's history: %q vs %q", first, other)

	// CancelTask interrupts the model call, not just the bookkeeping.
	out := a2aRPC(t, url, "SendMessage", map[string]any{
		"message":       a2aMsg("m4", "ctx-c", "block")["message"],
		"configuration": map[string]any{"returnImmediately": true},
	})
	st, _ := a2aResult(t, out)
	require.Equal(t, proto.StateWorking, st)
	id := out["result"].(map[string]any)["task"].(map[string]any)["id"]
	time.Sleep(200 * time.Millisecond) // let the turn reach the provider
	st, _ = a2aResult(t, a2aRPC(t, url, "CancelTask", map[string]any{"id": id}))
	assert.Equal(t, proto.StateCanceled, st)
	select {
	case <-prov.canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("model call was not interrupted by CancelTask")
	}
	require.Eventually(t, func() bool { // let the aborted turn settle
		n := 0
		al.activeTurnStates.Range(func(_, _ any) bool { n++; return true })
		return n == 0
	}, 5*time.Second, 20*time.Millisecond)
}
