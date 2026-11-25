package integrationtools

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/bus"
	a2achannel "github.com/sipeed/picoclaw/pkg/channels/a2a"
	"github.com/sipeed/picoclaw/pkg/config"
)

// startRemote runs a real inbound A2A channel whose "agent" echoes the request.
func startRemote(t *testing.T, token string) (url string) { return startGated(t, token, nil) }

// startGated is startRemote whose agent holds every answer until gate is closed.
func startGated(t *testing.T, token string, gate <-chan struct{}) (url string) {
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
			if gate != nil {
				<-gate
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
	require.Len(t, tools, 8)
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

func startAsync(t *testing.T, tools map[string]Tool, msg string) (*ToolResult, <-chan *ToolResult) {
	t.Helper()
	done := make(chan *ToolResult, 1)
	res := tools["a2a_call"].(AsyncExecutor).ExecuteAsync(context.Background(),
		map[string]any{"agent": "r", "message": msg, "async": true},
		func(_ context.Context, r *ToolResult) { done <- r })
	require.False(t, res.IsError, res.ForLLM)
	require.True(t, res.Async, "async call must return an AsyncResult")
	return res, done
}

func TestA2AAsyncCallPollStreamAndCallback(t *testing.T) {
	gate := make(chan struct{})
	tools := a2aTools(t, config.A2AAgents{"r": {URL: startGated(t, "", gate)}})
	ctx := context.Background()

	res, done := startAsync(t, tools, "slow job")
	id := fieldAfter(res.ForLLM, "task_id=")
	require.NotEmpty(t, id)

	res = tools["a2a_poll"].Execute(ctx, map[string]any{"agent": "r", "task_id": id})
	assert.False(t, res.IsError, res.ForLLM)
	assert.Contains(t, res.ForLLM, "state=working")
	assert.Contains(t, res.ForLLM, "(still running)")

	go func() { time.Sleep(100 * time.Millisecond); close(gate) }()
	res = tools["a2a_stream"].Execute(ctx, map[string]any{"agent": "r", "task_id": id}) // attaches and waits
	assert.False(t, res.IsError, res.ForLLM)
	assert.Contains(t, res.ForLLM, "state=completed")
	assert.Contains(t, res.ForLLM, "echo: slow job")

	select { // the async watcher reported on its own
	case r := <-done:
		assert.False(t, r.IsError, r.ForLLM)
		assert.Contains(t, r.ForLLM, "echo: slow job")
	case <-time.After(3 * time.Second):
		t.Fatal("async completion never reached the callback")
	}
	res = tools["a2a_poll"].Execute(ctx, map[string]any{"agent": "r", "task_id": id})
	assert.Contains(t, res.ForLLM, "state=completed")
	assert.NotContains(t, res.ForLLM, "(still running)")
}

func TestA2ACancelStopsAsyncTask(t *testing.T) {
	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	tools := a2aTools(t, config.A2AAgents{"r": {URL: startGated(t, "", gate)}})
	ctx := context.Background()

	res, done := startAsync(t, tools, "never answered")
	id := fieldAfter(res.ForLLM, "task_id=")

	res = tools["a2a_cancel"].Execute(ctx, map[string]any{"agent": "r", "task_id": id})
	assert.False(t, res.IsError, res.ForLLM)
	assert.Contains(t, res.ForLLM, "state=canceled")

	select {
	case r := <-done:
		assert.True(t, r.IsError, "a canceled task is reported as not completed")
		assert.Contains(t, r.ForLLM, "state=canceled")
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not notice the cancellation")
	}
	res = tools["a2a_cancel"].Execute(ctx, map[string]any{"agent": "r", "task_id": id})
	assert.True(t, res.IsError, "second cancel: task already finished")
}

func TestA2AStreamStartsTaskAndValidatesArgs(t *testing.T) {
	tools := a2aTools(t, config.A2AAgents{"r": {URL: startRemote(t, "")}})
	ctx := context.Background()

	res := tools["a2a_stream"].Execute(ctx, map[string]any{"agent": "r", "message": "hi"})
	assert.False(t, res.IsError, res.ForLLM)
	assert.Contains(t, res.ForLLM, "state=completed")
	assert.Contains(t, res.ForLLM, "echo: hi")

	for _, args := range []map[string]any{
		{"agent": "r"},
		{"agent": "r", "message": "x", "task_id": "y"},
	} {
		assert.True(t, tools["a2a_stream"].Execute(ctx, args).IsError, "%v", args)
	}
	assert.True(t, tools["a2a_poll"].Execute(ctx, map[string]any{"agent": "r"}).IsError)
	assert.True(t, tools["a2a_cancel"].Execute(ctx, map[string]any{"agent": "r", "task_id": "nope"}).IsError)
}

func TestA2AAsyncSlotsAreBounded(t *testing.T) {
	tools := a2aTools(t, config.A2AAgents{"r": {URL: startRemote(t, "")}})
	h := tools["a2a_call"].(*a2aCallTool).h
	for range a2aMaxWatch { // every watcher slot busy
		h.watch <- struct{}{}
	}
	res := tools["a2a_call"].(AsyncExecutor).ExecuteAsync(context.Background(),
		map[string]any{"agent": "r", "message": "one too many", "async": true}, func(context.Context, *ToolResult) {})
	assert.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "too many async")
	<-h.watch // a freed slot is usable again
	_, done := startAsync(t, tools, "fits")
	assert.NotNil(t, done)
}
