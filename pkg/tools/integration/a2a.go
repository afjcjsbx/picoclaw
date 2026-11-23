package integrationtools

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/a2a"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	a2aDefaultTimeout = 120 * time.Second
	a2aMaxResult      = 16 << 10 // bytes of remote text handed back to the model per call
	a2aMaxParallel    = 4        // concurrent remote calls in discover-all/orchestrate
	a2aMaxTasks       = 8        // sub-tasks per a2a_orchestrate call
	a2aRecentCap      = 32       // remembered calls (RAM only)
)

type a2aPeer struct {
	name    string
	cfg     config.A2AAgentConfig
	cl      *a2a.Client
	timeout time.Duration
}

type a2aCall struct {
	agent, contextID, taskID, state, request string
	at                                       time.Time
}

// a2aHub is the state shared by the five a2a_* tools: the configured peers and
// a small ring of recent calls. Nothing is persisted.
type a2aHub struct {
	peers  map[string]*a2aPeer
	mu     sync.Mutex
	recent []a2aCall
}

// NewA2ATools builds the a2a_* tools for the configured remote agents, or nil
// when none are usable.
func NewA2ATools(agents config.A2AAgents) []Tool {
	h := &a2aHub{peers: map[string]*a2aPeer{}}
	hc := &http.Client{} // per-call deadlines come from contexts
	for name, c := range agents {
		if c == nil {
			continue
		}
		if u, err := url.Parse(c.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			logger.WarnCF("a2a", "Skipping agent: url must be http(s)", map[string]any{"agent": name})
			continue
		}
		if t := strings.ToLower(c.Auth.Type); t != "" && t != "bearer" {
			logger.WarnCF("a2a", "Skipping agent: only bearer auth is supported", map[string]any{"agent": name})
			continue
		}
		timeout := a2aDefaultTimeout
		if c.Timeout > 0 {
			timeout = time.Duration(c.Timeout) * time.Second
		}
		h.peers[name] = &a2aPeer{
			name: name, cfg: *c, timeout: timeout,
			cl: &a2a.Client{HTTP: hc, BaseURL: c.URL, Token: c.Auth.Token.String()},
		}
	}
	if len(h.peers) == 0 {
		return nil
	}
	return []Tool{&a2aDiscoverTool{h}, &a2aCallTool{h}, &a2aListTool{h}, &a2aHistoryTool{h}, &a2aOrchestrateTool{h}}
}

func (h *a2aHub) names() string {
	names := make([]string, 0, len(h.peers))
	for n := range h.peers {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func (h *a2aHub) peer(args map[string]any) (*a2aPeer, *ToolResult) {
	name, _ := args["agent"].(string)
	p := h.peers[strings.TrimSpace(name)]
	if p == nil {
		return nil, ErrorResult(fmt.Sprintf("unknown agent %q; configured agents: %s", name, h.names()))
	}
	return p, nil
}

func (h *a2aHub) record(c a2aCall) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.recent) >= a2aRecentCap {
		h.recent = append(h.recent[:0], h.recent[1:]...)
	}
	h.recent = append(h.recent, c)
}

// ask runs one blocking exchange under the peer's deadline and records it.
func (h *a2aHub) ask(ctx context.Context, p *a2aPeer, contextID, msg string) (*a2a.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	t, err := p.cl.Ask(ctx, contextID, msg)
	rec := a2aCall{agent: p.name, request: a2aClipRunes(msg, 80), at: time.Now(), state: "error", contextID: contextID}
	if t != nil {
		rec.taskID, rec.state = t.ID, strings.TrimPrefix(t.Status.State, "TASK_STATE_")
		rec.contextID = t.ContextID
	}
	h.record(rec)
	return t, err
}

// format renders a task for the model.
func (p *a2aPeer) format(t *a2a.Task) string {
	state := strings.ToLower(strings.TrimPrefix(t.Status.State, "TASK_STATE_"))
	head := fmt.Sprintf("[%s] state=%s", p.name, state)
	if t.ContextID != "" {
		head += " context_id=" + t.ContextID
	}
	if t.ID != "" {
		head += " task_id=" + t.ID
	}
	if a2a.IsInterrupted(t.Status.State) {
		head += " (the agent needs more input: answer with a2a_call using the same context_id)"
	}
	return head + "\n" + a2aClipBytes(t.Text(), a2aMaxResult)
}

func a2aClipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "\n[truncated]"
}

func a2aClipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// a2aFanOut runs f(0..n-1) with at most a2aMaxParallel in flight.
func a2aFanOut(n int, f func(i int)) {
	sem := make(chan struct{}, a2aMaxParallel)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			f(i)
		}()
	}
	wg.Wait()
}

func a2aSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var (
	a2aAgentProp = map[string]any{"type": "string", "description": "Name of a configured remote agent"}
	a2aMsgProp   = map[string]any{"type": "string", "description": "The task or question, self-contained"}
)

// ---- a2a_discover ----

type a2aDiscoverTool struct{ h *a2aHub }

func (t *a2aDiscoverTool) Name() string { return "a2a_discover" }
func (t *a2aDiscoverTool) Description() string {
	return "Fetch the Agent Card of remote A2A agents to learn their skills before delegating. " +
		"Omit agent to inspect all. Configured agents: " + t.h.names()
}

func (t *a2aDiscoverTool) Parameters() map[string]any {
	return a2aSchema(map[string]any{"agent": a2aAgentProp})
}

func (t *a2aDiscoverTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	var peers []*a2aPeer
	if name, _ := args["agent"].(string); strings.TrimSpace(name) != "" {
		p, errRes := t.h.peer(args)
		if errRes != nil {
			return errRes
		}
		peers = []*a2aPeer{p}
	} else {
		for _, p := range t.h.peers {
			peers = append(peers, p)
		}
		sort.Slice(peers, func(i, j int) bool { return peers[i].name < peers[j].name })
	}
	out := make([]string, len(peers))
	a2aFanOut(len(peers), func(i int) {
		p := peers[i]
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		card, err := p.cl.Card(cctx)
		if err != nil {
			out[i] = fmt.Sprintf("[%s] unreachable: %v", p.name, err)
			return
		}
		var b strings.Builder
		fmt.Fprintf(&b, "[%s] %s v%s — %s\n", p.name, card.Name, card.Version, a2aClipRunes(card.Description, 300))
		fmt.Fprintf(&b, "  streaming=%v input=%s output=%s\n", card.Capabilities.Streaming,
			strings.Join(card.DefaultInputModes, ","), strings.Join(card.DefaultOutputModes, ","))
		for _, s := range card.Skills {
			fmt.Fprintf(&b, "  skill %s: %s — %s\n", s.ID, s.Name, a2aClipRunes(s.Description, 200))
		}
		if len(p.cfg.Capabilities) > 0 {
			fmt.Fprintf(&b, "  configured capabilities: %s\n", strings.Join(p.cfg.Capabilities, ", "))
		}
		out[i] = strings.TrimRight(b.String(), "\n")
	})
	return NewToolResult(strings.Join(out, "\n"))
}

// ---- a2a_call ----

type a2aCallTool struct{ h *a2aHub }

func (t *a2aCallTool) Name() string { return "a2a_call" }
func (t *a2aCallTool) Description() string {
	return "Send a task to a remote A2A agent and wait for its answer. Pass the context_id from a previous " +
		"answer to continue that conversation. Configured agents: " + t.h.names()
}

func (t *a2aCallTool) Parameters() map[string]any {
	return a2aSchema(map[string]any{
		"agent":      a2aAgentProp,
		"message":    a2aMsgProp,
		"context_id": map[string]any{"type": "string", "description": "Continue an earlier conversation (optional)"},
	}, "agent", "message")
}

func (t *a2aCallTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	p, errRes := t.h.peer(args)
	if errRes != nil {
		return errRes
	}
	msg, _ := args["message"].(string)
	if strings.TrimSpace(msg) == "" {
		return ErrorResult("message is required")
	}
	cid, _ := args["context_id"].(string)
	task, err := t.h.ask(ctx, p, strings.TrimSpace(cid), msg)
	if err != nil {
		return ErrorResult(fmt.Sprintf("a2a call to %s failed: %v", p.name, err))
	}
	res := p.format(task)
	if a2a.IsTerminal(task.Status.State) && task.Status.State != a2a.StateCompleted {
		return ErrorResult(res)
	}
	return NewToolResult(res)
}

// ---- a2a_list ----

type a2aListTool struct{ h *a2aHub }

func (t *a2aListTool) Name() string { return "a2a_list" }
func (t *a2aListTool) Description() string {
	return "List configured remote A2A agents (no network access) and the most recent conversations with them."
}
func (t *a2aListTool) Parameters() map[string]any { return a2aSchema(map[string]any{}) }

func (t *a2aListTool) Execute(context.Context, map[string]any) *ToolResult {
	var b strings.Builder
	for _, n := range strings.Split(t.h.names(), ", ") {
		p := t.h.peers[n]
		fmt.Fprintf(&b, "%s — %s", n, p.cfg.URL)
		if len(p.cfg.Capabilities) > 0 {
			fmt.Fprintf(&b, " — %s", strings.Join(p.cfg.Capabilities, ", "))
		}
		b.WriteByte('\n')
	}
	t.h.mu.Lock()
	defer t.h.mu.Unlock()
	if len(t.h.recent) > 0 {
		b.WriteString("recent conversations (newest first):\n")
	}
	for i := len(t.h.recent) - 1; i >= 0; i-- {
		c := t.h.recent[i]
		fmt.Fprintf(&b, "  %s %s state=%s context_id=%s task_id=%s %q\n",
			c.at.Format("15:04:05"), c.agent, c.state, c.contextID, c.taskID, c.request)
	}
	return NewToolResult(strings.TrimRight(b.String(), "\n"))
}

// ---- a2a_history ----

type a2aHistoryTool struct{ h *a2aHub }

func (t *a2aHistoryTool) Name() string { return "a2a_history" }
func (t *a2aHistoryTool) Description() string {
	return "Retrieve the message history of an earlier task from a remote A2A agent, by agent and task_id " +
		"(task ids appear in a2a_call and a2a_list output)."
}

func (t *a2aHistoryTool) Parameters() map[string]any {
	return a2aSchema(map[string]any{
		"agent":   a2aAgentProp,
		"task_id": map[string]any{"type": "string", "description": "Task id to fetch"},
		"limit": map[string]any{
			"type":        "integer",
			"description": "Most recent messages to return (default 10)",
			"minimum":     1.0,
			"maximum":     50.0,
		},
	}, "agent", "task_id")
}

func (t *a2aHistoryTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	p, errRes := t.h.peer(args)
	if errRes != nil {
		return errRes
	}
	id, _ := args["task_id"].(string)
	if strings.TrimSpace(id) == "" {
		return ErrorResult("task_id is required")
	}
	limit := 10
	if v, ok := args["limit"].(float64); ok && v >= 1 {
		limit = min(int(v), 50)
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	task, err := p.cl.GetTask(ctx, strings.TrimSpace(id), limit)
	if err != nil {
		return ErrorResult(fmt.Sprintf("a2a history from %s failed: %v", p.name, err))
	}
	var b strings.Builder
	fmt.Fprintf(
		&b,
		"[%s] task %s state=%s\n",
		p.name,
		task.ID,
		strings.ToLower(strings.TrimPrefix(task.Status.State, "TASK_STATE_")),
	)
	for _, m := range task.History {
		fmt.Fprintf(&b, "%s: %s\n", strings.ToLower(strings.TrimPrefix(m.Role, "ROLE_")), m.Text())
	}
	return NewToolResult(a2aClipBytes(b.String(), a2aMaxResult))
}

// ---- a2a_orchestrate ----

type a2aOrchestrateTool struct{ h *a2aHub }

func (t *a2aOrchestrateTool) Name() string { return "a2a_orchestrate" }
func (t *a2aOrchestrateTool) Description() string {
	return fmt.Sprintf(
		"Run independent tasks on several remote A2A agents in parallel (max %d per call, %d at a time) "+
			"and collect all answers. Each task starts a fresh conversation. Configured agents: %s",
		a2aMaxTasks,
		a2aMaxParallel,
		t.h.names(),
	)
}

func (t *a2aOrchestrateTool) Parameters() map[string]any {
	return a2aSchema(map[string]any{"tasks": map[string]any{
		"type":        "array",
		"description": "Independent tasks, one per remote agent call",
		"maxItems":    a2aMaxTasks,
		"items":       a2aSchema(map[string]any{"agent": a2aAgentProp, "message": a2aMsgProp}, "agent", "message"),
	}}, "tasks")
}

func (t *a2aOrchestrateTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	raw, _ := args["tasks"].([]any)
	if len(raw) == 0 || len(raw) > a2aMaxTasks {
		return ErrorResult(fmt.Sprintf("tasks must contain 1-%d items", a2aMaxTasks))
	}
	type job struct {
		peer *a2aPeer
		msg  string
	}
	jobs := make([]job, len(raw))
	for i, r := range raw {
		m, _ := r.(map[string]any)
		p, errRes := t.h.peer(m)
		if errRes != nil {
			return errRes
		}
		msg, _ := m["message"].(string)
		if strings.TrimSpace(msg) == "" {
			return ErrorResult(fmt.Sprintf("tasks[%d].message is required", i))
		}
		jobs[i] = job{p, msg}
	}
	out := make([]string, len(jobs))
	failed := 0
	var mu sync.Mutex
	a2aFanOut(len(jobs), func(i int) {
		task, err := t.h.ask(ctx, jobs[i].peer, "", jobs[i].msg)
		switch {
		case err != nil:
			out[i] = fmt.Sprintf("[%s] failed: %v", jobs[i].peer.name, err)
		case a2a.IsTerminal(task.Status.State) && task.Status.State != a2a.StateCompleted:
			out[i] = jobs[i].peer.format(task)
		default:
			out[i] = jobs[i].peer.format(task)
			return
		}
		mu.Lock()
		failed++
		mu.Unlock()
	})
	res := strings.Join(out, "\n\n")
	if failed == len(jobs) {
		return ErrorResult(res)
	}
	return NewToolResult(res)
}
