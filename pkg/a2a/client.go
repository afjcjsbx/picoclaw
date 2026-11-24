package a2a

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// maxResponseBytes bounds every remote response held in RAM.
const maxResponseBytes = 1 << 20

// pollStart is the first wait before polling an unfinished task (a var for tests).
var pollStart = time.Second

// Client talks JSON-RPC to one remote agent. Safe for concurrent use.
type Client struct {
	HTTP    *http.Client
	BaseURL string // e.g. http://host:9900
	Token   string // bearer token, optional

	mu       sync.Mutex
	endpoint string
}

// NewID returns a random hex identifier.
func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Card fetches the public Agent Card.
func (c *Client) Card(ctx context.Context) (*AgentCard, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(c.BaseURL, "/")+WellKnownPath, nil)
	if err != nil {
		return nil, err
	}
	var card AgentCard
	if err := c.do(req, &card); err != nil {
		return nil, err
	}
	return &card, nil
}

// rpcURL resolves (once) the JSON-RPC endpoint. The card's URL is honored only
// on the configured origin: otherwise a hostile card could redirect the bearer
// token to a third party.
func (c *Client) rpcURL(ctx context.Context) string {
	c.mu.Lock()
	ep := c.endpoint
	c.mu.Unlock()
	if ep != "" {
		return ep
	}
	// Fetch without holding mu: a slow card must not block callers that have
	// their own ctx. Racing callers may fetch twice; the first result wins.
	fallback := strings.TrimRight(c.BaseURL, "/") + RPCPath
	card, err := c.Card(ctx)
	if err != nil {
		return fallback // not cached: retry discovery next call
	}
	ep = fallback
	base, _ := url.Parse(c.BaseURL)
	for _, i := range card.SupportedInterfaces {
		u, err := url.Parse(i.URL)
		if i.ProtocolBinding == BindingJSONRPC && err == nil && base != nil &&
			u.Scheme == base.Scheme && u.Host == base.Host {
			ep = i.URL
			break
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.endpoint == "" {
		c.endpoint = ep
	}
	return c.endpoint
}

// Call performs one JSON-RPC request and decodes result into out (may be nil).
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rpcURL(ctx), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *RPCError       `json:"error"`
	}
	if err := c.do(req, &env); err != nil {
		return err
	}
	if env.Error != nil {
		return env.Error
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}

func (c *Client) do(req *http.Request, out any) error {
	req.Header.Set("Accept", "application/json")
	resp, err := c.roundTrip(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return httpError(resp.StatusCode, data)
	}
	return json.Unmarshal(data, out)
}

func (c *Client) roundTrip(req *http.Request) (*http.Response, error) {
	req.Header[VersionHeader] = []string{Version} // set raw: keeps the spec's spelling on the wire
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	return hc.Do(req)
}

// httpError prefers the JSON-RPC error carried by a non-2xx reply.
func httpError(status int, data []byte) error {
	var env struct {
		Error *RPCError `json:"error"`
	}
	if json.Unmarshal(data, &env) == nil && env.Error != nil {
		return env.Error
	}
	return fmt.Errorf("http %d: %s", status, truncate(string(data), 200))
}

// Stream calls a streaming method (SendStreamingMessage, SubscribeToTask) and
// hands each frame to fn until fn returns false, the stream ends or ctx is done.
// The read blocks on the socket: no timers or polling while the remote works.
// Memory is one frame at a time (<= maxResponseBytes).
func (c *Client) Stream(ctx context.Context, method string, params any, fn func(*StreamResult) bool) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rpcURL(ctx), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.roundTrip(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		if resp.StatusCode/100 != 2 {
			return httpError(resp.StatusCode, data)
		}
		var env struct {
			Error *RPCError `json:"error"`
		}
		if json.Unmarshal(data, &env) == nil && env.Error != nil {
			return env.Error
		}
		return errors.New("peer did not stream")
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 4<<10), maxResponseBytes)
	var data []byte
	for sc.Scan() {
		line := sc.Bytes()
		if v, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			v = bytes.TrimPrefix(v, []byte(" "))
			if len(data)+1+len(v) > maxResponseBytes { // the scanner caps a line, this caps the whole frame
				return errors.New("a2a: stream frame too large")
			}
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, v...)
			continue
		}
		if len(line) > 0 || len(data) == 0 { // comments (keepalive) and other fields are ignored
			continue
		}
		var env struct {
			Result *StreamResult `json:"result"`
			Error  *RPCError     `json:"error"`
		}
		err := json.Unmarshal(data, &env)
		data = data[:0]
		switch {
		case err != nil:
			return err
		case env.Error != nil:
			return env.Error
		case env.Result != nil && !fn(env.Result):
			return nil
		}
	}
	return sc.Err()
}

// Send submits a text message. Blocking unless cfg.ReturnImmediately.
func (c *Client) Send(ctx context.Context, contextID, text string, cfg *SendConfig) (*SendResult, error) {
	var res SendResult
	err := c.Call(ctx, "SendMessage", SendParams{
		Message: Message{
			MessageID: NewID(), ContextID: contextID, Role: RoleUser, Parts: []Part{TextPart(text)},
		},
		Configuration: cfg,
	}, &res)
	return &res, err
}

// GetTask fetches a task; historyLength < 0 omits history.
func (c *Client) GetTask(ctx context.Context, id string, historyLength int) (*Task, error) {
	p := map[string]any{"id": id}
	if historyLength >= 0 {
		p["historyLength"] = historyLength
	}
	var t Task
	return &t, c.Call(ctx, "GetTask", p, &t)
}

// CancelTask asks the remote agent to stop a task and returns its new state.
func (c *Client) CancelTask(ctx context.Context, id string) (*Task, error) {
	var t Task
	return &t, c.Call(ctx, "CancelTask", map[string]any{"id": id}, &t)
}

// CancelDetached cancels best-effort, even when ctx is already done.
func (c *Client) CancelDetached(ctx context.Context, id string) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = c.CancelTask(cctx, id)
}

// Wait blocks until t is settled and returns its final state without history.
// It subscribes first (event-driven: the socket stays idle while the remote
// works), then falls back to GetTask with a 1s→5s backoff for peers that do not
// stream or dropped the link. It never cancels the remote task: that is the
// caller's call. On ctx expiry it returns the last known task and ctx.Err().
func (c *Client) Wait(ctx context.Context, t *Task) (*Task, error) {
	if IsSettled(t.Status.State) {
		return t, nil
	}
	// Errors are fine here: the GetTask below is authoritative.
	_ = c.Stream(ctx, "SubscribeToTask", map[string]any{"id": t.ID}, func(r *StreamResult) bool {
		return !IsSettled(r.State())
	})
	for wait := pollStart; ; wait = min(wait*2, 5*time.Second) {
		next, err := c.GetTask(ctx, t.ID, 0)
		switch {
		case ctx.Err() != nil:
			return t, ctx.Err()
		case err != nil:
			return t, err
		}
		if t = next; IsSettled(t.Status.State) {
			return t, nil
		}
		select {
		case <-ctx.Done():
			return t, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// Ask sends text and waits for a settled task. On ctx expiry the remote task
// is canceled best-effort. Returns the last known task even on error.
func (c *Client) Ask(ctx context.Context, contextID, text string) (*Task, error) {
	res, err := c.Send(ctx, contextID, text, &SendConfig{AcceptedOutputModes: []string{"text/plain"}})
	if err != nil {
		return nil, err
	}
	if res.Task == nil {
		// Plain message reply: wrap it so callers see one shape.
		return &Task{
			ContextID: contextID,
			Status:    TaskStatus{State: StateCompleted, Message: res.Message},
		}, nil
	}
	t, err := c.Wait(ctx, res.Task)
	if err != nil && ctx.Err() != nil {
		c.CancelDetached(ctx, t.ID)
	}
	return t, err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
