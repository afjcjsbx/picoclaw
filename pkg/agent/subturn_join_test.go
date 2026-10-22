package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// joinProvider scripts root -> researcher -> two slow leaves. The researcher
// answers directly right after spawning; the test passes only if the SubTurn
// is held back and gets another LLM call that sees both leaf results.
type joinProvider struct {
	mu       sync.Mutex
	resCalls int
	resSaw   int // "[SubTurn Result]" messages seen by the researcher's last call
	resDone  chan struct{}
}

func (p *joinProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	model string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	spawn := func(id, agent string) providers.ToolCall {
		return providers.ToolCall{
			ID: id, Name: "spawn",
			Arguments: map[string]any{"task": "task for " + agent, "agent_id": agent},
		}
	}
	hasTool := false
	for _, m := range messages {
		hasTool = hasTool || m.Role == "tool"
	}

	switch model {
	case "m-root":
		if !hasTool {
			return &providers.LLMResponse{ToolCalls: []providers.ToolCall{spawn("r", "researcher")}}, nil
		}
	case "m-res":
		p.mu.Lock()
		defer p.mu.Unlock()
		p.resCalls++
		if p.resCalls == 1 {
			return &providers.LLMResponse{
				ToolCalls: []providers.ToolCall{spawn("a", "leaf1"), spawn("b", "leaf2")},
			}, nil
		}
		p.resSaw = 0
		for _, m := range messages {
			if strings.HasPrefix(m.Content, "[SubTurn Result]") {
				p.resSaw++
			}
		}
		if p.resSaw >= 2 {
			select {
			case <-p.resDone:
			default:
				close(p.resDone)
			}
		}
	case "m-leaf":
		select {
		case <-time.After(150 * time.Millisecond):
		case <-ctx.Done():
		}
	}
	return &providers.LLMResponse{Content: "done by " + model}, nil
}

func (p *joinProvider) GetDefaultModel() string { return "m-root" }

func TestSubTurnJoinsAsyncChildren(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = tmp
	cfg.Agents.Defaults.ModelName = "m-root"
	ag := func(id, model string, allow ...string) config.AgentConfig {
		a := config.AgentConfig{ID: id, Workspace: tmp + "/" + id, Model: &config.AgentModelConfig{Primary: model}}
		if len(allow) > 0 {
			a.Subagents = &config.SubagentsConfig{AllowAgents: allow}
		}
		return a
	}
	root := ag("root", "m-root", "researcher")
	root.Default = true
	cfg.Agents.List = []config.AgentConfig{
		root,
		ag("researcher", "m-res", "leaf1", "leaf2"),
		ag("leaf1", "m-leaf"),
		ag("leaf2", "m-leaf"),
	}

	prov := &joinProvider{resDone: make(chan struct{})}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), prov)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := al.ProcessDirect(ctx, "go", "join-test"); err != nil {
		t.Fatalf("ProcessDirect: %v", err)
	}

	select {
	case <-prov.resDone:
	case <-time.After(5 * time.Second):
		prov.mu.Lock()
		defer prov.mu.Unlock()
		t.Fatalf("researcher finished without joining its children (llm calls=%d, results seen=%d)",
			prov.resCalls, prov.resSaw)
	}
}

type capProvider func(ctx context.Context, model string, msgs []providers.Message) *providers.LLMResponse

func (f capProvider) Chat(
	ctx context.Context, msgs []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any,
) (*providers.LLMResponse, error) {
	return f(ctx, model, msgs), nil
}
func (f capProvider) GetDefaultModel() string { return "m-root" }

// A SubTurn that leaves through the iteration cap (no direct answer, so no
// join point in the LLM loop) must still outlive its async children.
func TestSubTurnOutlivesChildrenOnIterationCap(t *testing.T) {
	var slowDone atomic.Int32
	spawn := func(id, agent string) *providers.LLMResponse {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
			ID: id, Name: "spawn", Arguments: map[string]any{"task": "go", "agent_id": agent},
		}}}
	}
	prov := capProvider(func(ctx context.Context, model string, msgs []providers.Message) *providers.LLMResponse {
		switch model {
		case "m-root":
			for _, m := range msgs {
				if m.Role == "tool" {
					return &providers.LLMResponse{Content: "ack"}
				}
			}
			return spawn("r", "boss")
		case "m-boss": // never answers directly
			time.Sleep(100 * time.Millisecond)
			return spawn(fmt.Sprintf("b%d", time.Now().UnixNano()), "slow")
		default: // slow
			select {
			case <-time.After(300 * time.Millisecond):
			case <-ctx.Done():
			}
			slowDone.Add(1)
			return &providers.LLMResponse{Content: "slow done"}
		}
	})

	tmp := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = tmp
	cfg.Agents.Defaults.ModelName = "m-root"
	cfg.Agents.Defaults.MaxToolIterations = 2
	ag := func(id string, allow ...string) config.AgentConfig {
		a := config.AgentConfig{ID: id, Workspace: tmp + "/" + id, Model: &config.AgentModelConfig{Primary: "m-" + id}}
		if len(allow) > 0 {
			a.Subagents = &config.SubagentsConfig{AllowAgents: allow}
		}
		return a
	}
	root := ag("root", "boss")
	root.Default = true
	cfg.Agents.List = []config.AgentConfig{root, ag("boss", "slow"), ag("slow")}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), prov)
	defer al.Close()

	if _, err := al.ProcessDirect(context.Background(), "go", "cap-test"); err != nil {
		t.Fatal(err)
	}
	bossActive := func() bool {
		found := false
		al.activeTurnStates.Range(func(_, v any) bool {
			if ts, ok := v.(*turnState); ok && ts.agentID == "boss" {
				found = true
			}
			return true
		})
		return found
	}
	deadline := time.Now().Add(5 * time.Second)
	for !bossActive() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	for bossActive() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if got := slowDone.Load(); got != 2 {
		t.Fatalf("boss ended with %d/2 children done: a SubTurn must not end before its async children", got)
	}
}

// HardAbort on a root turn must cancel the sub-turns it started, even when the
// root is blocked in a synchronous delegate.
func TestRootHardAbortCancelsSubTurns(t *testing.T) {
	var canceled, timedOut, bossCalls atomic.Int32
	slowExited := make(chan struct{}, 2)
	spawnSlow := func(id string) providers.ToolCall {
		return providers.ToolCall{ID: id, Name: "spawn", Arguments: map[string]any{"task": "block", "agent_id": "slow"}}
	}
	prov := capProvider(func(ctx context.Context, model string, msgs []providers.Message) *providers.LLMResponse {
		switch model {
		case "m-root":
			for _, m := range msgs {
				if m.Role == "tool" {
					return &providers.LLMResponse{Content: "root done"}
				}
			}
			return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
				ID: "d", Name: "delegate", Arguments: map[string]any{"agent_id": "boss", "task": "go"},
			}}}
		case "m-boss":
			if bossCalls.Add(1) == 1 {
				return &providers.LLMResponse{ToolCalls: []providers.ToolCall{spawnSlow("a"), spawnSlow("b")}}
			}
			return &providers.LLMResponse{Content: "boss premature"}
		default: // slow: blocks until canceled (3s guard so a regression fails fast)
			select {
			case <-time.After(3 * time.Second):
				timedOut.Add(1)
			case <-ctx.Done():
				canceled.Add(1)
			}
			slowExited <- struct{}{}
			return &providers.LLMResponse{Content: "slow out"}
		}
	})

	tmp := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = tmp
	cfg.Agents.Defaults.ModelName = "m-root"
	ag := func(id string, allow ...string) config.AgentConfig {
		a := config.AgentConfig{ID: id, Workspace: tmp + "/" + id, Model: &config.AgentModelConfig{Primary: "m-" + id}}
		if len(allow) > 0 {
			a.Subagents = &config.SubagentsConfig{AllowAgents: allow}
		}
		return a
	}
	root := ag("root", "boss")
	root.Default = true
	cfg.Agents.List = []config.AgentConfig{root, ag("boss", "slow"), ag("slow")}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), prov)
	defer al.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = al.ProcessDirect(context.Background(), "go", "abort-test")
	}()

	var sessionKey string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		n := 0
		al.activeTurnStates.Range(func(_, v any) bool {
			if ts, ok := v.(*turnState); ok {
				n++
				if ts.depth == 0 {
					sessionKey = ts.sessionKey
				}
			}
			return true
		})
		if n >= 4 { // root + boss + 2 slow
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if err := al.HardAbort(sessionKey); err != nil {
		t.Fatalf("HardAbort: %v", err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ProcessDirect did not return after HardAbort")
	}
	// Cancellation is asynchronous: the root loop can return before the child
	// provider goroutines observe ctx.Done(). Wait for both leaves to exit
	// before asserting on their counters.
	for range 2 {
		select {
		case <-slowExited:
		case <-time.After(5 * time.Second):
			t.Fatalf(
				"sub-turns did not stop after HardAbort (canceled=%d timedOut=%d)",
				canceled.Load(),
				timedOut.Load(),
			)
		}
	}
	if canceled.Load() != 2 {
		t.Fatalf(
			"root HardAbort canceled %d/2 sub-turns (%d ran to their own timeout)",
			canceled.Load(),
			timedOut.Load(),
		)
	}
}
