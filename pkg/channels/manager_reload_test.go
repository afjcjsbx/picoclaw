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

	if channelPresent(m, reloadTestChannel) {
		t.Fatal("channel was not removed after a reload disabling it")
	}
	if w := workerFor(t, m, reloadTestChannel); w != nil {
		t.Fatal("worker was not removed after a reload disabling its channel")
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
