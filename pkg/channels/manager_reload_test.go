package channels

import (
	"context"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

const reloadTestChannel = "reload_unit_channel"

func newReloadTestConfig(t *testing.T, settings string, enabled bool) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	bc := &config.Channel{Enabled: enabled, Type: reloadTestChannel}
	bc.SetName(reloadTestChannel)
	if settings != "" {
		bc.Settings = config.RawNode(settings)
	}
	cfg.Channels = config.ChannelsConfig{reloadTestChannel: bc}
	return cfg
}

func newReloadTestManager(t *testing.T, cfg *config.Config) *Manager {
	t.Helper()
	RegisterFactory(reloadTestChannel, func(
		_ string, _ string, _ *config.Config, _ *bus.MessageBus,
	) (Channel, error) {
		return &mockChannel{}, nil
	})

	m, err := NewManager(cfg, bus.NewMessageBus(), nil)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	m.mux = newDynamicServeMux()
	if err := m.StartAll(context.Background()); err != nil {
		t.Fatalf("StartAll() error = %v", err)
	}
	return m
}

func workerFor(t *testing.T, m *Manager, name string) *channelWorker {
	t.Helper()
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.workers[name]
}

func channelPresent(m *Manager, name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.channels[name]
	return ok
}

// eventually polls cond until it holds or the deadline passes. Retiring a
// channel finishes with a map update that happens after the worker's done
// channel closes, so tests must not assume it is visible at that point.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitWorkerRetired blocks until the reload cleanup goroutine has finished
// draining the given worker (its done channel is closed).
func waitWorkerRetired(t *testing.T, w *channelWorker) {
	t.Helper()
	if w == nil {
		return
	}
	select {
	case <-w.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for retired worker goroutine to stop")
	}
}

// A reload that changes an existing channel's settings restarts that channel
// under the same name. The replacement must keep a working worker.
func TestReload_ChangedChannelKeepsWorker(t *testing.T) {
	m := newReloadTestManager(t, newReloadTestConfig(t, `{"v":1}`, true))

	oldWorker := workerFor(t, m, reloadTestChannel)
	if oldWorker == nil {
		t.Fatal("expected a worker after StartAll")
	}

	if err := m.Reload(context.Background(), newReloadTestConfig(t, `{"v":2}`, true)); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	waitWorkerRetired(t, oldWorker)

	newWorker := workerFor(t, m, reloadTestChannel)
	if newWorker == nil {
		t.Fatal("channel lost its worker after a config-changing reload")
	}
	if newWorker == oldWorker {
		t.Fatal("worker was not replaced after a config-changing reload")
	}
	if !channelPresent(m, reloadTestChannel) {
		t.Fatal("channel missing after a config-changing reload")
	}

	if err := m.SendMessage(context.Background(), bus.OutboundMessage{
		Context: bus.InboundContext{Channel: reloadTestChannel},
		ChatID:  "1",
		Content: "hello",
	}); err != nil {
		t.Fatalf("SendMessage() after reload error = %v", err)
	}
}

// A reload that removes a channel must tear down both the channel and its worker.
func TestReload_RemovedChannelCleansUp(t *testing.T) {
	m := newReloadTestManager(t, newReloadTestConfig(t, `{"v":1}`, true))

	oldWorker := workerFor(t, m, reloadTestChannel)
	if oldWorker == nil {
		t.Fatal("expected a worker after StartAll")
	}

	if err := m.Reload(context.Background(), newReloadTestConfig(t, `{"v":1}`, false)); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	waitWorkerRetired(t, oldWorker)

	eventually(t, "channel removal after a reload disabling it", func() bool {
		return !channelPresent(m, reloadTestChannel)
	})
	eventually(t, "worker removal after a reload disabling its channel", func() bool {
		return workerFor(t, m, reloadTestChannel) == nil
	})
}

// Draining a retired worker can take as long as its send retries. It must not
// hold m.mu meanwhile, or the outbound dispatchers (which take m.mu.RLock for
// every message) stall for every other channel.
func TestRetireChannelInstance_DoesNotHoldLockWhileDraining(t *testing.T) {
	m := newReloadTestManager(t, newReloadTestConfig(t, `{"v":1}`, true))

	slow := &channelWorker{
		done:      make(chan struct{}),
		mediaDone: make(chan struct{}),
		stop:      make(chan struct{}),
	}
	retired := make(chan struct{})
	go func() {
		defer close(retired)
		m.retireChannelInstance("slow_channel", nil, slow)
	}()

	// shutdown() closes stop right before the drain wait, so once stop is closed
	// the retire goroutine is blocked waiting for the worker to drain.
	select {
	case <-slow.stop:
	case <-time.After(2 * time.Second):
		t.Fatal("retireChannelInstance did not start draining the worker")
	}

	// The worker is still draining: the manager lock must be free.
	locked := make(chan struct{})
	go func() {
		m.mu.RLock()
		m.mu.RUnlock()
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(2 * time.Second):
		t.Fatal("retireChannelInstance holds m.mu while waiting for the worker to drain")
	}

	select {
	case <-retired:
		t.Fatal("retireChannelInstance returned before the worker finished draining")
	default:
	}

	close(slow.done)
	close(slow.mediaDone)
	select {
	case <-retired:
	case <-time.After(2 * time.Second):
		t.Fatal("retireChannelInstance did not return after the worker drained")
	}
}

// A reload that does not touch the channel must keep the very same worker.
func TestReload_UnchangedChannelKeepsSameWorker(t *testing.T) {
	m := newReloadTestManager(t, newReloadTestConfig(t, `{"v":1}`, true))

	oldWorker := workerFor(t, m, reloadTestChannel)
	if oldWorker == nil {
		t.Fatal("expected a worker after StartAll")
	}

	if err := m.Reload(context.Background(), newReloadTestConfig(t, `{"v":1}`, true)); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if got := workerFor(t, m, reloadTestChannel); got != oldWorker {
		t.Fatalf("unchanged channel worker was replaced: got %p want %p", got, oldWorker)
	}
}

// A reload must reuse the manager lifecycle context so StopAll keeps owning
// every goroutine it may need to cancel; replacing it would orphan the previous
// cancel func on each reload.
func TestReload_ReusesLifecycleContext(t *testing.T) {
	m := newReloadTestManager(t, newReloadTestConfig(t, `{"v":1}`, true))

	m.mu.RLock()
	beforeCtx := m.dispatchCtx
	beforeTask := m.dispatchTask
	m.mu.RUnlock()
	if beforeCtx == nil || beforeTask == nil {
		t.Fatal("expected a lifecycle context after StartAll")
	}

	if err := m.Reload(context.Background(), newReloadTestConfig(t, `{"v":2}`, true)); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	m.mu.RLock()
	afterCtx := m.dispatchCtx
	afterTask := m.dispatchTask
	m.mu.RUnlock()
	if afterCtx != beforeCtx {
		t.Fatal("reload replaced the lifecycle context")
	}
	if afterTask != beforeTask {
		t.Fatal("reload replaced the lifecycle dispatch task")
	}
}
