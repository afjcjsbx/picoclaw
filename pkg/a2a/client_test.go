package a2a

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fake serves a card advertising cardURL and dispatches JSON-RPC to handle.
func fake(t *testing.T, cardURL string, handle func(method string, params json.RawMessage) any) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+WellKnownPath, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(AgentCard{
			Name:                "fake",
			SupportedInterfaces: []Interface{{URL: cardURL, ProtocolBinding: BindingJSONRPC, ProtocolVersion: Version}},
		})
	})
	mux.HandleFunc("POST "+RPCPath, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).
			Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": handle(req.Method, req.Params)})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCardEndpointOnAnotherOriginIsIgnored(t *testing.T) {
	// A hostile card must not be able to steer the bearer token to a third host.
	srv := fake(t, "http://evil.invalid/a2a", func(string, json.RawMessage) any { return map[string]any{} })
	c := &Client{BaseURL: srv.URL, Token: "t"}
	assert.Equal(t, srv.URL+RPCPath, c.rpcURL(context.Background()))
}

func TestCardEndpointOnSameOriginIsUsed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+WellKnownPath, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(AgentCard{SupportedInterfaces: []Interface{
			{URL: "http://" + r.Host + "/custom", ProtocolBinding: BindingJSONRPC},
		}})
	})
	srv2 := httptest.NewServer(mux)
	defer srv2.Close()
	c := &Client{BaseURL: srv2.URL}
	assert.Equal(t, srv2.URL+"/custom", c.rpcURL(context.Background()))
}

func TestSlowCardDoesNotBlockOtherCallers(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // runs before srv.Close
	c := &Client{BaseURL: srv.URL}

	go c.rpcURL(context.Background()) // first caller sits on the slow card
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan string, 1)
	go func() { done <- c.rpcURL(ctx) }()
	select {
	case got := <-done:
		assert.Equal(t, srv.URL+RPCPath, got)
	case <-time.After(2 * time.Second):
		t.Fatal("second caller blocked behind the first caller's card fetch")
	}
}

func TestAskPollsUntilTerminal(t *testing.T) {
	pollStart = 5 * time.Millisecond
	t.Cleanup(func() { pollStart = time.Second })
	var gets atomic.Int32
	srv := fake(t, "", func(method string, _ json.RawMessage) any {
		switch method {
		case "SendMessage":
			return SendResult{Task: &Task{ID: "t1", Status: TaskStatus{State: StateWorking}}}
		case "GetTask":
			if gets.Add(1) < 3 {
				return Task{ID: "t1", Status: TaskStatus{State: StateWorking}}
			}
			return Task{
				ID:        "t1",
				Status:    TaskStatus{State: StateCompleted},
				Artifacts: []Artifact{{Parts: []Part{TextPart("done")}}},
			}
		}
		return nil
	})
	task, err := (&Client{BaseURL: srv.URL}).Ask(context.Background(), "", "go")
	require.NoError(t, err)
	assert.Equal(t, StateCompleted, task.Status.State)
	assert.Equal(t, "done", task.Text())
	assert.EqualValues(t, 3, gets.Load())
}

func TestAskCancelsRemoteTaskOnTimeout(t *testing.T) {
	pollStart = 5 * time.Millisecond
	t.Cleanup(func() { pollStart = time.Second })
	var canceled atomic.Bool
	srv := fake(t, "", func(method string, _ json.RawMessage) any {
		switch method {
		case "CancelTask":
			canceled.Store(true)
		case "SendMessage":
			return SendResult{Task: &Task{ID: "t1", Status: TaskStatus{State: StateWorking}}}
		}
		return Task{ID: "t1", Status: TaskStatus{State: StateWorking}}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	_, err := (&Client{BaseURL: srv.URL}).Ask(ctx, "", "go")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.True(t, canceled.Load(), "remote task must not be left running")
}

func TestPlainMessageReplyIsWrappedAsTask(t *testing.T) {
	srv := fake(t, "", func(string, json.RawMessage) any {
		return SendResult{Message: &Message{Role: RoleAgent, Parts: []Part{TextPart("hi")}}}
	})
	task, err := (&Client{BaseURL: srv.URL}).Ask(context.Background(), "c", "yo")
	require.NoError(t, err)
	assert.Equal(t, StateCompleted, task.Status.State)
	assert.Equal(t, "hi", task.Text())
}
