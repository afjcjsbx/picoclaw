package integrationtools

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/bus"
	a2achannel "github.com/sipeed/picoclaw/pkg/channels/a2a"
	"github.com/sipeed/picoclaw/pkg/config"
)

// startRemote runs a real inbound A2A channel whose "agent" echoes the request.
func startRemote(t *testing.T, token string) (url string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	b := bus.NewMessageBus()
	t.Cleanup(b.Close)
	cfg := &config.A2ASettings{Port: port}
	if token != "" {
		cfg.Tokens = config.SimpleSecureStrings(token)
	}
	ch, err := a2achannel.New(&config.Channel{}, cfg, b)
	require.NoError(t, err)
	require.NoError(t, ch.Start(context.Background()))
	t.Cleanup(func() { _ = ch.Stop(context.Background()) })

	go func() {
		for m := range b.InboundChan() {
			if m.Content == "/stop" {
				continue
			}
			_, _ = ch.Send(context.Background(), bus.OutboundMessage{
				ChatID: m.ChatID, Content: "echo: " + m.Content,
				Context: bus.InboundContext{Raw: map[string]string{"outbound_kind": "final"}},
			})
		}
	}()
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

func a2aTools(t *testing.T, agents config.A2AAgents) map[string]Tool {
	t.Helper()
	out := map[string]Tool{}
	for _, tool := range NewA2ATools(agents) {
		out[tool.Name()] = tool
	}
	return out
}

func TestA2AToolsAgainstRealChannel(t *testing.T) {
	url := startRemote(t, "s3cret")
	var tok config.SecureString
	tok.Set("s3cret")
	tools := a2aTools(t, config.A2AAgents{
		"researcher": {
			URL:          url,
			Auth:         config.A2AAuthConfig{Type: "bearer", Token: tok},
			Capabilities: []string{"research"},
		},
		"nokey": {URL: url}, // same server, no token: must be refused
	})
	require.Len(t, tools, 5)
	ctx := context.Background()

	res := tools["a2a_discover"].Execute(ctx, map[string]any{"agent": "researcher"})
	assert.False(t, res.IsError, res.ForLLM)
	assert.Contains(t, res.ForLLM, "PicoClaw")
	assert.Contains(t, res.ForLLM, "skill assistant")
	assert.Contains(t, res.ForLLM, "configured capabilities: research")

	res = tools["a2a_call"].Execute(ctx, map[string]any{"agent": "researcher", "message": "hello"})
	require.False(t, res.IsError, res.ForLLM)
	assert.Contains(t, res.ForLLM, "state=completed")
	assert.Contains(t, res.ForLLM, "echo: hello")

	res = tools["a2a_call"].Execute(ctx, map[string]any{"agent": "nokey", "message": "hello"})
	assert.True(t, res.IsError, "missing token must fail, not hang")

	res = tools["a2a_call"].Execute(ctx, map[string]any{"agent": "ghost", "message": "x"})
	assert.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "researcher")

	res = tools["a2a_list"].Execute(ctx, nil)
	assert.Contains(t, res.ForLLM, "researcher — "+url)
	assert.Contains(t, res.ForLLM, "recent conversations")

	taskID := fieldAfter(
		tools["a2a_call"].Execute(ctx, map[string]any{"agent": "researcher", "message": "again"}).ForLLM,
		"task_id=",
	)
	require.NotEmpty(t, taskID)
	res = tools["a2a_history"].Execute(ctx, map[string]any{"agent": "researcher", "task_id": taskID})
	assert.False(t, res.IsError, res.ForLLM)
	assert.Contains(t, res.ForLLM, "user: again")
	assert.Contains(t, res.ForLLM, "agent: echo: again")

	res = tools["a2a_orchestrate"].Execute(ctx, map[string]any{"tasks": []any{
		map[string]any{"agent": "researcher", "message": "one"},
		map[string]any{"agent": "researcher", "message": "two"},
	}})
	assert.False(t, res.IsError, res.ForLLM)
	assert.Contains(t, res.ForLLM, "echo: one")
	assert.Contains(t, res.ForLLM, "echo: two")

	res = tools["a2a_orchestrate"].Execute(ctx, map[string]any{"tasks": []any{}})
	assert.True(t, res.IsError)
}

func TestA2AToolsSkipBadConfigAndAbsentWithoutAgents(t *testing.T) {
	assert.Nil(t, NewA2ATools(nil))
	assert.Nil(t, NewA2ATools(config.A2AAgents{
		"a": {URL: "ftp://x"},
		"b": {URL: "http://x", Auth: config.A2AAuthConfig{Type: "oauth"}},
	}))
}

func fieldAfter(s, key string) string {
	_, rest, ok := strings.Cut(s, key)
	if !ok {
		return ""
	}
	return strings.Fields(rest)[0]
}
