// Package a2a is the inbound A2A channel: it exposes PicoClaw as an A2A v1.0
// server (JSON-RPC binding) so other agents can discover it and hand it tasks.
//
// Resource model: one listener goroutine, one goroutine per live HTTP request
// (net/http), tasks in a bounded in-RAM registry, no polling loops. A task
// costs one registry entry (<= ~66 KiB, normally far less) and one timer; its
// push webhooks (<= 3, ~2 KiB each) are freed when it finishes.
package a2a

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	proto "github.com/sipeed/picoclaw/pkg/a2a"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	defaultPort        = 9900
	defaultBody        = 1 << 20
	defaultRate        = 60
	defaultMaxTasks    = 256
	defaultConcurrent  = 4
	defaultTaskTimeout = 300 * time.Second
	heartbeat          = 20 * time.Second
	stopCommand        = "/stop"
)

type principal struct {
	id  string
	sum [32]byte
}

// Channel implements channels.Channel (+ StreamingCapable) for A2A.
type Channel struct {
	*channels.BaseChannel
	cfg     *config.A2ASettings
	addr    string
	timeout time.Duration
	maxBody int64
	maxConc int
	tokens  []principal
	reg     *registry
	rl      *limiter
	push    *http.Client  // webhook deliveries (SSRF-guarded dialer)
	pushSem chan struct{} // bounds concurrent deliveries

	mu   sync.Mutex
	srv  *http.Server
	ctx  context.Context
	quit chan struct{}
}

// New validates cfg and builds the channel; nothing is bound until Start.
func New(bc *config.Channel, cfg *config.A2ASettings, b *bus.MessageBus) (*Channel, error) {
	host, port := cfg.Host, cfg.Port
	if host == "" {
		host = "127.0.0.1"
	}
	if port == 0 {
		port = defaultPort
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("a2a: invalid port %d", port)
	}
	c := &Channel{
		// Access control is the bearer token, not allow_from.
		BaseChannel: channels.NewBaseChannel("a2a", cfg, b, []string{"*"},
			channels.WithReasoningChannelID(bc.ReasoningChannelID)),
		cfg:     cfg,
		addr:    net.JoinHostPort(host, fmt.Sprint(port)),
		timeout: time.Duration(orDefault(cfg.TaskTimeoutSeconds, int(defaultTaskTimeout/time.Second))) * time.Second,
		maxBody: int64(orDefault(cfg.MaxBodyBytes, defaultBody)),
		maxConc: orDefault(cfg.MaxConcurrent, defaultConcurrent),
		reg:     newRegistry(orDefault(cfg.MaxTasks, defaultMaxTasks)),
		rl:      &limiter{max: orDefault(cfg.RateLimitPerMinute, defaultRate), win: map[string]*window{}},
		pushSem: make(chan struct{}, pushParallel),
	}
	c.push = c.newPushClient()
	c.reg.onDone = c.notify
	for _, tok := range cfg.Tokens.Values() {
		if tok != "" {
			sum := sha256.Sum256([]byte(tok))
			c.tokens = append(c.tokens, principal{id: hex.EncodeToString(sum[:4]), sum: sum})
		}
	}
	// No token means "localhost only": refuse to expose an open agent on the network.
	if len(c.tokens) == 0 && !isLoopback(host) {
		return nil, fmt.Errorf("a2a: host %q is not loopback; configure tokens to expose the agent", host)
	}
	return c, nil
}

func orDefault(v, d int) int {
	if v > 0 {
		return v
	}
	return d
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

func (c *Channel) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", c.addr)
	if err != nil {
		return fmt.Errorf("a2a: listen %s: %w", c.addr, err)
	}
	srv := &http.Server{
		Handler:           c.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second, // request only; replies may take as long as a task
		MaxHeaderBytes:    16 << 10,
	}
	c.mu.Lock()
	c.srv, c.ctx, c.quit = srv, ctx, make(chan struct{})
	c.mu.Unlock()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorCF("a2a", "A2A server stopped", map[string]any{"error": err.Error()})
		}
	}()
	c.SetRunning(true)
	logger.InfoCF("a2a", "A2A channel listening", map[string]any{"addr": c.addr, "auth": len(c.tokens) > 0})
	return nil
}

func (c *Channel) Stop(ctx context.Context) error {
	c.SetRunning(false)
	c.mu.Lock()
	srv, quit := c.srv, c.quit
	c.srv = nil
	c.mu.Unlock()
	if srv == nil {
		return nil
	}
	close(quit) // unblocks SSE handlers and blocking SendMessage waiters
	for _, t := range c.reg.closeAll() {
		c.stopTurn(t)
	}
	// Handlers already left via quit; the grace only covers idle/never-used conns.
	sctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return srv.Close()
	}
	return nil
}

// start hands a registered task to the agent loop and arms its deadline.
func (c *Channel) start(t *task, text string) {
	c.reg.transition(t, proto.StateWorking, "")
	c.reg.arm(t, c.timeout, func() { c.abort(t, proto.StateFailed, "task timed out") })
	err := c.HandleInboundContext(c.runCtx(), t.chatID, text, nil, bus.InboundContext{
		ChatID: t.chatID, ChatType: "direct", SenderID: t.principal, MessageID: t.messageID,
	})
	if err != nil {
		c.reg.transition(t, proto.StateFailed, "agent unavailable: "+err.Error())
	}
}

// abort ends a running task and interrupts its agent turn (best effort: the
// agent checks between iterations, a blocked external command may linger).
func (c *Channel) abort(t *task, state, reason string) bool {
	if !c.reg.transition(t, state, reason) {
		return false
	}
	c.stopTurn(t)
	return true
}

// stopTurn reuses the agent's /stop command, so no agent-loop coupling is needed.
func (c *Channel) stopTurn(t *task) {
	_ = c.HandleInboundContext(c.runCtx(), t.chatID, stopCommand, nil, bus.InboundContext{
		ChatID: t.chatID, ChatType: "direct", SenderID: t.principal,
	})
}

func (c *Channel) quitCh() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.quit
}

func (c *Channel) runCtx() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

// Send receives agent output addressed to a task's chat. Tool progress becomes
// a status note; the turn's final reply (outbound_kind=final) completes the task.
// Anything for a chat without a running task (e.g. the /stop acknowledgement)
// is dropped.
func (c *Channel) Send(_ context.Context, msg bus.OutboundMessage) ([]string, error) {
	t := c.running(msg.ChatID)
	if t == nil || strings.TrimSpace(msg.Content) == "" {
		return nil, nil
	}
	switch kind := msg.Context.Raw["message_kind"]; {
	case kind == "tool_feedback" || kind == "tool_calls":
		c.reg.progress(t, msg.Content)
	case kind != "":
		// approvals and other control messages have no meaning for a remote agent
	case msg.Context.Raw["outbound_kind"] == "final":
		c.reg.finish(t, msg.Content)
	default:
		// mid-turn text (message tool): keep it as reply content
		c.reg.appendText(t, msg.Content, true)
	}
	return nil, nil
}

func (c *Channel) running(chatID string) *task {
	c.reg.mu.Lock()
	defer c.reg.mu.Unlock()
	return c.reg.byChat[chatID]
}

// BeginStream opts a turn into token streaming only when a client is attached
// to the task; blocking SendMessage callers get the plain, cheaper path.
func (c *Channel) BeginStream(_ context.Context, chatID string) (channels.Streamer, error) {
	t := c.running(chatID)
	if t == nil || !c.reg.isStreaming(t) {
		return nil, errors.New("a2a: no streaming client")
	}
	return &streamer{c: c, t: t}, nil
}

// streamer turns the agent's cumulative text into append-only deltas.
// A new streamer starts per LLM call; the previous one was canceled when the
// model went on to call tools, so its text stays and the next one is separated.
type streamer struct {
	c    *Channel
	t    *task
	prev string
}

func (s *streamer) Update(_ context.Context, acc string) error {
	if d, ok := strings.CutPrefix(acc, s.prev); ok && d != "" {
		s.c.reg.appendText(s.t, d, s.prev == "")
		s.prev = acc
	}
	return nil
}

func (s *streamer) Finalize(ctx context.Context, content string) error {
	_ = s.Update(ctx, content)
	s.c.reg.finish(s.t, "")
	return nil
}

func (s *streamer) Cancel(context.Context) {}
