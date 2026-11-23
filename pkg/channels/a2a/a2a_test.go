package a2a

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	proto "github.com/sipeed/picoclaw/pkg/a2a"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

type rig struct {
	t   *testing.T
	ch  *Channel
	b   *bus.MessageBus
	url string
}

// newRig serves the channel's routes without binding the real listener.
func newRig(t *testing.T, cfg config.A2ASettings) *rig {
	t.Helper()
	b := bus.NewMessageBus()
	t.Cleanup(b.Close)
	ch, err := New(&config.Channel{}, &cfg, b)
	require.NoError(t, err)
	srv := httptest.NewServer(ch.routes())
	t.Cleanup(srv.Close)
	return &rig{t: t, ch: ch, b: b, url: srv.URL}
}

// agent plays the agent loop: it answers the next inbound message on chat.
func (r *rig) agent(answer string) <-chan bus.InboundMessage {
	got := make(chan bus.InboundMessage, 1)
	go func() {
		select {
		case m := <-r.b.InboundChan():
			got <- m
			_, _ = r.ch.Send(context.Background(), bus.OutboundMessage{
				ChatID:  m.ChatID,
				Content: answer,
				Context: bus.InboundContext{Raw: map[string]string{"outbound_kind": "final"}},
			})
		case <-time.After(5 * time.Second):
		}
	}()
	return got
}

func (r *rig) rpc(token, method string, params any) (int, map[string]any) {
	r.t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 7, "method": method, "params": params})
	req, _ := http.NewRequest(http.MethodPost, r.url+proto.RPCPath, strings.NewReader(string(body)))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(r.t, err)
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func msg(id, ctx, text string) map[string]any {
	m := map[string]any{"messageId": id, "role": proto.RoleUser, "parts": []any{map[string]any{"text": text}}}
	if ctx != "" {
		m["contextId"] = ctx
	}
	return map[string]any{"message": m}
}

func taskOf(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	res, ok := out["result"].(map[string]any)
	require.True(t, ok, "no result in %v", out)
	if tk, ok := res["task"].(map[string]any); ok {
		return tk
	}
	return res
}

func state(tk map[string]any) string { return tk["status"].(map[string]any)["state"].(string) }

func errCode(out map[string]any) int {
	e, _ := out["error"].(map[string]any)
	c, _ := e["code"].(float64)
	return int(c)
}

func TestAgentCardIsPublicAndWellFormed(t *testing.T) {
	r := newRig(t, config.A2ASettings{Tokens: config.SimpleSecureStrings("tok"), URL: "https://agent.example/"})
	resp, err := http.Get(r.url + proto.WellKnownPath) // no Authorization
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	var card proto.AgentCard
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&card))
	assert.Equal(t, "https://agent.example/a2a", card.SupportedInterfaces[0].URL)
	assert.Equal(t, proto.BindingJSONRPC, card.SupportedInterfaces[0].ProtocolBinding)
	assert.True(t, card.Capabilities.Streaming)
	assert.Contains(t, card.SecuritySchemes, "bearerAuth")
	assert.NotEmpty(t, card.Skills)
}

func TestNonLoopbackWithoutTokenIsRefused(t *testing.T) {
	_, err := New(&config.Channel{}, &config.A2ASettings{Host: "0.0.0.0"}, bus.NewMessageBus())
	require.Error(t, err)
	_, err = New(
		&config.Channel{},
		&config.A2ASettings{Host: "0.0.0.0", Tokens: config.SimpleSecureStrings("x")},
		bus.NewMessageBus(),
	)
	require.NoError(t, err)
}

func TestAuth(t *testing.T) {
	r := newRig(t, config.A2ASettings{Tokens: config.SimpleSecureStrings("good")})
	code, _ := r.rpc("", "ListTasks", nil)
	assert.Equal(t, 401, code)
	code, _ = r.rpc("bad", "ListTasks", nil)
	assert.Equal(t, 401, code)
	code, out := r.rpc("good", "ListTasks", nil)
	assert.Equal(t, 200, code)
	assert.Nil(t, out["error"])
}

func TestSendMessageBlockingAndIdempotent(t *testing.T) {
	r := newRig(t, config.A2ASettings{})
	got := r.agent("pong")
	_, out := r.rpc("", "SendMessage", msg("m1", "c1", "ping"))
	tk := taskOf(t, out)
	assert.Equal(t, proto.StateCompleted, state(tk))
	arts := tk["artifacts"].([]any)
	assert.Equal(t, "pong", arts[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"])
	hist := tk["history"].([]any)
	assert.Len(t, hist, 2)

	in := <-got
	assert.Equal(t, "ping", in.Content)
	assert.Equal(t, "a2a", in.Channel)

	// Retry with the same messageId: same task, no second turn.
	_, out2 := r.rpc("", "SendMessage", msg("m1", "c1", "ping"))
	assert.Equal(t, tk["id"], taskOf(t, out2)["id"])
	select {
	case m := <-r.b.InboundChan():
		t.Fatalf("retry published a second turn: %q", m.Content)
	case <-time.After(100 * time.Millisecond):
	}

	// A retry may omit contextId, but must not name a different one.
	_, out2 = r.rpc("", "SendMessage", msg("m1", "", "ping"))
	assert.Equal(t, tk["id"], taskOf(t, out2)["id"])
	_, out2 = r.rpc("", "SendMessage", msg("m1", "c2", "ping"))
	assert.Equal(t, proto.CodeInvalidParams, errCode(out2), "messageId reused in another context")

	// historyLength 0 drops history, 1 keeps the reply only.
	_, out3 := r.rpc("", "GetTask", map[string]any{"id": tk["id"], "historyLength": 1})
	h := taskOf(t, out3)["history"].([]any)
	require.Len(t, h, 1)
	assert.Equal(t, proto.RoleAgent, h[0].(map[string]any)["role"])
	_, out4 := r.rpc("", "GetTask", map[string]any{"id": tk["id"], "historyLength": 0})
	assert.Nil(t, taskOf(t, out4)["history"])
}

func TestVersionHeader(t *testing.T) {
	r := newRig(t, config.A2ASettings{})
	for v, ok := range map[string]bool{"": true, "1.0": true, "1.0.0": true, "1": false, "1.1": false, "2.0": false} {
		body := `{"jsonrpc":"2.0","id":1,"method":"ListTasks","params":{}}`
		req, _ := http.NewRequest(http.MethodPost, r.url+proto.RPCPath, strings.NewReader(body))
		if v != "" {
			req.Header[proto.VersionHeader] = []string{v}
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if ok {
			assert.Nil(t, out["error"], "A2A-Version %q", v)
		} else {
			assert.Equal(t, proto.CodeVersion, errCode(out), "A2A-Version %q", v)
		}
	}
}

func TestRequestValidation(t *testing.T) {
	r := newRig(t, config.A2ASettings{})
	_, out := r.rpc("", "SendMessage", map[string]any{"message": map[string]any{"role": proto.RoleUser}})
	assert.Equal(t, proto.CodeInvalidParams, errCode(out), "missing messageId")

	m := msg("m", "", "x")
	m["message"].(map[string]any)["parts"] = []any{map[string]any{"raw": "AAAA", "mediaType": "image/png"}}
	_, out = r.rpc("", "SendMessage", m)
	assert.Equal(t, proto.CodeContentType, errCode(out), "non-text part")

	_, out = r.rpc("", "SendMessage", msg("m", "bad ctx!", "x"))
	assert.Equal(t, proto.CodeInvalidParams, errCode(out), "contextId charset")

	m = msg("m", "", "x")
	m["configuration"] = map[string]any{"acceptedOutputModes": []any{"image/png"}}
	_, out = r.rpc("", "SendMessage", m)
	assert.Equal(t, proto.CodeContentType, errCode(out), "output modes")

	_, out = r.rpc("", "message/send", msg("m", "", "x"))
	assert.Equal(t, proto.CodeVersion, errCode(out), "slash-style method name")
	_, out = r.rpc("", "Nope", nil)
	assert.Equal(t, proto.CodeMethodNotFound, errCode(out))
	_, out = r.rpc("", "CreateTaskPushNotificationConfig", nil)
	assert.Equal(t, proto.CodePushUnsupported, errCode(out))
	_, out = r.rpc("", "GetTask", map[string]any{"id": "nope"})
	assert.Equal(t, proto.CodeTaskNotFound, errCode(out))
}

func TestTasksAreScopedPerToken(t *testing.T) {
	r := newRig(t, config.A2ASettings{Tokens: config.SimpleSecureStrings("alice", "bob")})
	r.agent("hi")
	_, out := r.rpc("alice", "SendMessage", msg("m1", "", "hello"))
	id := taskOf(t, out)["id"]

	_, out = r.rpc("bob", "GetTask", map[string]any{"id": id})
	assert.Equal(t, proto.CodeTaskNotFound, errCode(out))
	_, out = r.rpc("bob", "CancelTask", map[string]any{"id": id})
	assert.Equal(t, proto.CodeTaskNotFound, errCode(out))
	_, out = r.rpc("bob", "ListTasks", nil)
	assert.EqualValues(t, 0, out["result"].(map[string]any)["totalSize"])
	_, out = r.rpc("alice", "ListTasks", nil)
	assert.EqualValues(t, 1, out["result"].(map[string]any)["totalSize"])
}

func TestCancelRunningTaskInterruptsTurn(t *testing.T) {
	r := newRig(t, config.A2ASettings{})
	_, out := r.rpc("", "SendMessage", map[string]any{
		"message": msg("m1", "c1", "slow")["message"], "configuration": map[string]any{"returnImmediately": true},
	})
	tk := taskOf(t, out)
	assert.Equal(t, proto.StateWorking, state(tk))
	<-r.b.InboundChan() // the turn

	// Same context is busy until the task ends.
	_, out = r.rpc("", "SendMessage", msg("m2", "c1", "again"))
	assert.Equal(t, codeContextBusy, errCode(out))

	_, out = r.rpc("", "CancelTask", map[string]any{"id": tk["id"]})
	assert.Equal(t, proto.StateCanceled, state(taskOf(t, out)))
	stop := <-r.b.InboundChan()
	assert.Equal(t, "/stop", stop.Content)
	assert.Equal(t, "a2a", stop.Channel)

	_, out = r.rpc("", "CancelTask", map[string]any{"id": tk["id"]})
	assert.Equal(t, proto.CodeTaskNotCancelable, errCode(out))

	// A late reply for a canceled task must not revive it.
	_, _ = r.ch.Send(context.Background(), bus.OutboundMessage{
		ChatID: stop.ChatID, Content: "late",
		Context: bus.InboundContext{Raw: map[string]string{"outbound_kind": "final"}},
	})
	_, out = r.rpc("", "GetTask", map[string]any{"id": tk["id"]})
	assert.Equal(t, proto.StateCanceled, state(taskOf(t, out)))
}

func TestTaskTimeoutFailsTask(t *testing.T) {
	r := newRig(t, config.A2ASettings{})
	r.ch.timeout = 50 * time.Millisecond
	_, out := r.rpc("", "SendMessage", msg("m1", "", "never answered"))
	tk := taskOf(t, out)
	assert.Equal(t, proto.StateFailed, state(tk))
	assert.Contains(
		t,
		tk["status"].(map[string]any)["message"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"],
		"timed out",
	)
}

func TestRateLimitAndConcurrencyCap(t *testing.T) {
	r := newRig(t, config.A2ASettings{RateLimitPerMinute: 3, MaxConcurrent: 1})
	opt := map[string]any{"returnImmediately": true}
	m := msg("a", "c1", "x")
	m["configuration"] = opt
	_, out := r.rpc("", "SendMessage", m) // call 1 occupies the only slot
	require.Nil(t, out["error"])
	m2 := msg("b", "c2", "y")
	m2["configuration"] = opt
	code, _ := r.rpc("", "SendMessage", m2) // call 2: no slot left
	assert.Equal(t, 503, code)
	code, _ = r.rpc("", "ListTasks", nil) // call 3
	assert.Equal(t, 200, code)
	code, _ = r.rpc("", "ListTasks", nil) // call 4 > 3/min
	assert.Equal(t, 429, code)
}

func TestRegistryEvictsOldestTerminalAndRefusesWhenFull(t *testing.T) {
	reg := newRegistry(2)
	a, _, err := reg.submit("p", "1", "c1", "a", false, 10)
	require.NoError(t, err)
	b, _, err := reg.submit("p", "2", "c2", "b", false, 10)
	require.NoError(t, err)
	_, _, err = reg.submit("p", "3", "c3", "c", false, 10)
	assert.ErrorIs(t, err, errFull, "all slots running")

	reg.finish(a, "done")
	_, _, err = reg.submit("p", "3", "c3", "c", false, 10)
	require.NoError(t, err)
	assert.Nil(t, reg.get("p", a.id), "oldest terminal task evicted")
	assert.NotNil(t, reg.get("p", b.id))
}

func TestStreamingEndToEnd(t *testing.T) {
	r := newRig(t, config.A2ASettings{})
	body, _ := json.Marshal(
		map[string]any{
			"jsonrpc": "2.0",
			"id":      "s1",
			"method":  "SendStreamingMessage",
			"params":  msg("m1", "c1", "stream me"),
		},
	)
	req, _ := http.NewRequest(http.MethodPost, r.url+proto.RPCPath, strings.NewReader(string(body)))
	resp, err := http.DefaultClient.Do(req) //nolint:bodyclose // see defer
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	frames := make(chan map[string]any, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var f map[string]any
				_ = json.Unmarshal([]byte(data), &f)
				frames <- f
			}
		}
		close(frames)
	}()
	next := func() map[string]any {
		select {
		case f := <-frames:
			return f
		case <-time.After(3 * time.Second):
			t.Fatal("no frame")
			return nil
		}
	}

	f := next() // snapshot
	assert.Equal(t, "s1", f["id"], "request id is echoed on every frame")
	first := f["result"].(map[string]any)["task"].(map[string]any)
	assert.Equal(t, proto.StateWorking, state(first), "snapshot already carries the working state")

	in := <-r.b.InboundChan()
	st, err := r.ch.BeginStream(context.Background(), in.ChatID)
	require.NoError(t, err)
	_ = st.Update(context.Background(), "he")
	_ = st.Update(context.Background(), "hello")
	_ = st.Finalize(context.Background(), "hello")

	var text strings.Builder
	var sawLast bool
	for {
		f := next()
		res := f["result"].(map[string]any)
		if u, ok := res["artifactUpdate"].(map[string]any); ok {
			text.WriteString(u["artifact"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string))
			sawLast = sawLast || u["lastChunk"] == true
		}
		if s, ok := res["statusUpdate"].(map[string]any); ok {
			switch s["status"].(map[string]any)["state"] {
			case proto.StateCompleted:
				assert.Equal(t, "hello", text.String(), "deltas concatenate to the reply, no duplication")
				assert.True(t, sawLast, "last chunk flagged")
				return
			}
		}
	}
}

func TestBeginStreamOnlyForStreamingClients(t *testing.T) {
	r := newRig(t, config.A2ASettings{})
	m := msg("m1", "c1", "x")
	m["configuration"] = map[string]any{"returnImmediately": true}
	r.rpc("", "SendMessage", m)
	in := <-r.b.InboundChan()
	_, err := r.ch.BeginStream(context.Background(), in.ChatID)
	assert.Error(t, err, "blocking callers take the cheaper non-streaming path")
}

func TestStartStopBindsAndReleasesPort(t *testing.T) {
	b := bus.NewMessageBus()
	defer b.Close()
	ch, err := New(&config.Channel{}, &config.A2ASettings{Port: freePort(t)}, b)
	require.NoError(t, err)
	require.NoError(t, ch.Start(context.Background()))
	resp, err := http.Get("http://" + ch.addr + proto.WellKnownPath)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	require.NoError(t, ch.Stop(context.Background()))
	if c, err := net.DialTimeout("tcp", ch.addr, time.Second); err == nil {
		c.Close()
		t.Fatal("port still accepting after Stop")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}
