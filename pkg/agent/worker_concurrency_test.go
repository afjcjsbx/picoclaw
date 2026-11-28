package agent

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

type workerGateProvider struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
	active  atomic.Int32
	max     atomic.Int32
}

func (p *workerGateProvider) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	active := p.active.Add(1)
	defer p.active.Add(-1)
	for {
		currentMax := p.max.Load()
		if active <= currentMax || p.max.CompareAndSwap(currentMax, active) {
			break
		}
	}
	select {
	case p.started <- struct{}{}:
	default:
	}
	select {
	case <-p.release:
		return &providers.LLMResponse{Content: "ok"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *workerGateProvider) GetDefaultModel() string { return "test-model" }

func workerTestConfig(t *testing.T, parallel int) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.Defaults.MaxParallelTurns = parallel
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "test-model",
		Model:     "test/model",
	}}
	return cfg
}

func publishWorkerTestMessage(t *testing.T, msgBus *bus.MessageBus, session string) {
	t.Helper()
	if err := msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel:    "test",
		ChatID:     session,
		SenderID:   "user",
		SessionKey: session,
		Content:    "hello",
		Context: bus.InboundContext{
			Channel:  "test",
			ChatID:   session,
			SenderID: "user",
		},
	}); err != nil {
		t.Fatalf("PublishInbound() error = %v", err)
	}
}

func waitWorkerRun(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("AgentLoop.Run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for AgentLoop.Run() to exit")
	}
}

func TestAgentWorkerSemaphoreCapsConcurrentTurns(t *testing.T) {
	provider := &workerGateProvider{
		started: make(chan struct{}, 4),
		release: make(chan struct{}),
	}
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(workerTestConfig(t, 2), msgBus, provider)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- al.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		al.Stop()
		al.Close()
		msgBus.Close()
	})

	for i := range 4 {
		publishWorkerTestMessage(t, msgBus, fmt.Sprintf("session-%d", i))
	}
	for range 2 {
		select {
		case <-provider.started:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for initial workers")
		}
	}
	select {
	case <-provider.started:
		t.Fatal("third turn started while both worker slots were occupied")
	case <-time.After(100 * time.Millisecond):
	}

	close(provider.release)
	for range 2 {
		select {
		case <-provider.started:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for queued workers")
		}
	}
	if got := provider.calls.Load(); got != 4 {
		t.Fatalf("provider calls = %d, want 4", got)
	}
	if got := provider.max.Load(); got > 2 {
		t.Fatalf("maximum concurrent provider calls = %d, want <= 2", got)
	}

	cancel()
	al.Stop()
	waitWorkerRun(t, done)
}

func TestAgentShutdownReleasesActiveAndQueuedWorkers(t *testing.T) {
	provider := &workerGateProvider{
		started: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(workerTestConfig(t, 1), msgBus, provider)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- al.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		al.Stop()
		al.Close()
		msgBus.Close()
	})

	publishWorkerTestMessage(t, msgBus, "active-session")
	publishWorkerTestMessage(t, msgBus, "queued-session")
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for active worker")
	}

	cancel()
	al.Stop()
	waitWorkerRun(t, done)

	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want only the active worker", got)
	}
	if al.getActiveTurnState("active-session") != nil {
		t.Fatal("active session turn state survived shutdown")
	}
	if al.getActiveTurnState("queued-session") != nil {
		t.Fatal("queued session placeholder survived shutdown")
	}
	if got := len(al.workerSem); got != 0 {
		t.Fatalf("worker semaphore slots in use after shutdown = %d", got)
	}
}
