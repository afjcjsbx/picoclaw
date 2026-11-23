package a2a

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
	defer c.mu.Unlock()
	if c.endpoint != "" {
		return c.endpoint
	}
	fallback := strings.TrimRight(c.BaseURL, "/") + RPCPath
	card, err := c.Card(ctx)
	if err != nil {
		return fallback // not cached: retry discovery next call
	}
	c.endpoint = fallback
	base, _ := url.Parse(c.BaseURL)
	for _, i := range card.SupportedInterfaces {
		u, err := url.Parse(i.URL)
		if i.ProtocolBinding == BindingJSONRPC && err == nil && base != nil &&
			u.Scheme == base.Scheme && u.Host == base.Host {
			c.endpoint = i.URL
			break
		}
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
	req.Header[VersionHeader] = []string{Version} // set raw: keeps the spec's spelling on the wire
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return err
	}
	// JSON-RPC errors may ride on non-2xx; let the envelope speak when it parses.
	if resp.StatusCode/100 != 2 {
		var env struct {
			Error *RPCError `json:"error"`
		}
		if json.Unmarshal(data, &env) == nil && env.Error != nil {
			return env.Error
		}
		return fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(data), 200))
	}
	return json.Unmarshal(data, out)
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

// CancelTask asks the remote agent to stop a task.
func (c *Client) CancelTask(ctx context.Context, id string) error {
	return c.Call(ctx, "CancelTask", map[string]any{"id": id}, nil)
}

// Ask sends text and waits for a settled task: servers that answer before the
// task is done are polled with a 1s→5s backoff. On ctx expiry the remote task
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
	t := res.Task
	for wait := pollStart; !IsTerminal(t.Status.State) && !IsInterrupted(t.Status.State); {
		select {
		case <-ctx.Done():
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_ = c.CancelTask(cctx, t.ID)
			cancel()
			return t, ctx.Err()
		case <-time.After(wait):
		}
		next, err := c.GetTask(ctx, t.ID, -1)
		if err != nil {
			return t, err
		}
		t = next
		wait = min(wait*2, 5*time.Second)
	}
	return t, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
