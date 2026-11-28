package gateway

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/health"
	"github.com/sipeed/picoclaw/pkg/heartbeat"
	"github.com/sipeed/picoclaw/pkg/media"
	"github.com/sipeed/picoclaw/pkg/netbind"
	"github.com/sipeed/picoclaw/pkg/providers"
)

type lifecycleTestProvider struct {
	reason string
	closes atomic.Int32
}

func (p *lifecycleTestProvider) Chat(
	context.Context,
	[]providers.Message,
	[]providers.ToolDefinition,
	string,
	map[string]any,
) (*providers.LLMResponse, error) {
	return nil, errors.New(p.reason)
}

func (p *lifecycleTestProvider) GetDefaultModel() string { return "lifecycle-test" }
func (p *lifecycleTestProvider) Close()                  { p.closes.Add(1) }

func stubCreateProvider(
	t *testing.T,
	fn func(*config.Config) (providers.LLMProvider, string, error),
) {
	t.Helper()
	original := createProvider
	createProvider = fn
	t.Cleanup(func() { createProvider = original })
}

func newLifecycleFixture(t *testing.T) (*config.Config, *agent.AgentLoop, *bus.MessageBus, *services) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Heartbeat.Enabled = true
	msgBus := bus.NewMessageBus()
	oldProvider := &lifecycleTestProvider{reason: "old provider"}
	al := agent.NewAgentLoop(cfg, msgBus, oldProvider)
	manager, err := channels.NewManager(cfg, msgBus, media.NewFileMediaStore())
	if err != nil {
		t.Fatalf("channels.NewManager() error = %v", err)
	}
	runningServices := &services{
		ChannelManager: manager,
		MediaStore:     media.NewFileMediaStore(),
		authToken:      "test-token",
	}
	runningServices.HeartbeatService = heartbeat.NewHeartbeatService(
		cfg.WorkspacePath(),
		cfg.Power.EffectiveHeartbeatInterval(cfg.Heartbeat.Interval),
		cfg.Heartbeat.Enabled,
	)
	runningServices.HeartbeatService.SetBus(msgBus)
	runningServices.HeartbeatService.SetHandler(createHeartbeatHandler(al))
	if err := runningServices.HeartbeatService.Start(); err != nil {
		t.Fatalf("HeartbeatService.Start() error = %v", err)
	}
	t.Cleanup(func() {
		stopAndCleanupServices(runningServices, time.Second, false)
		al.Close()
		msgBus.Close()
	})
	return cfg, al, msgBus, runningServices
}

func TestSetupAndStartServicesRollsBackPartialStartup(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Heartbeat.Enabled = true
	cfg.Channels[config.ChannelOneBot] = &config.Channel{
		Enabled:  true,
		Type:     config.ChannelOneBot,
		Settings: config.RawNode(`{"ws_url":"://bad","reconnect_interval":0}`),
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := agent.NewAgentLoop(cfg, msgBus, &lifecycleTestProvider{reason: "unused"})
	t.Cleanup(al.Close)

	runningServices, err := setupAndStartServices(cfg, al, msgBus, "token", netbind.OpenResult{})
	if err == nil {
		t.Fatal("setupAndStartServices() error = nil, want channel initialization error")
	}
	if runningServices == nil {
		t.Fatal("setupAndStartServices() returned nil services on partial startup")
	}
	if runningServices.HeartbeatService == nil || runningServices.HeartbeatService.IsRunning() {
		t.Fatal("heartbeat service still running after partial startup rollback")
	}
	if runningServices.getCronService() == nil {
		t.Fatal("cron service was not recorded for rollback")
	}
	if runningServices.ChannelManager == nil {
		t.Fatal("channel manager should be retained for startup rollback")
	}
}

func TestExecuteReloadProviderFailureRestoresServices(t *testing.T) {
	_, al, msgBus, runningServices := newLifecycleFixture(t)
	runningServices.HealthServer = health.NewServer("127.0.0.1", 0, "token")
	oldProvider := providers.LLMProvider(&lifecycleTestProvider{reason: "old provider"})
	providerRef := oldProvider
	stubCreateProvider(t, func(*config.Config) (providers.LLMProvider, string, error) {
		return nil, "", errors.New("provider init failed")
	})

	err := executeReload(
		context.Background(),
		al,
		config.DefaultConfig(),
		&providerRef,
		runningServices,
		msgBus,
		false,
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "provider init failed") {
		t.Fatalf("executeReload() error = %v, want provider init failure", err)
	}
	if providerRef != oldProvider {
		t.Fatal("provider reference changed after failed reload")
	}
	if runningServices.HeartbeatService == nil || !runningServices.HeartbeatService.IsRunning() {
		t.Fatal("heartbeat service was not restored after failed reload")
	}
	if runningServices.getCronService() == nil {
		t.Fatal("cron service was not restored after failed reload")
	}
	if runningServices.reloading.Load() {
		t.Fatal("reloading flag was not reset")
	}
	status := runningServices.HealthServer.GetReloadStatus()
	if status.InProgress || !strings.Contains(status.LastError, "provider init failed") {
		t.Fatalf("reload status = %+v", status)
	}
}

func TestExecuteReloadAgentFailureClosesNewProviderAndRestoresServices(t *testing.T) {
	_, al, msgBus, runningServices := newLifecycleFixture(t)
	runningServices.HealthServer = health.NewServer("127.0.0.1", 0, "token")
	oldProvider := providers.LLMProvider(&lifecycleTestProvider{reason: "old provider"})
	providerRef := oldProvider
	newProvider := &lifecycleTestProvider{reason: "new provider"}
	stubCreateProvider(t, func(*config.Config) (providers.LLMProvider, string, error) {
		return newProvider, "new-model", nil
	})
	newCfg := config.DefaultConfig()
	newCfg.Hooks.Processes = map[string]config.ProcessHookConfig{
		"invalid-hook": {
			Enabled:   true,
			Command:   []string{"echo"},
			Intercept: []string{"not_supported"},
		},
	}

	err := executeReload(
		context.Background(),
		al,
		newCfg,
		&providerRef,
		runningServices,
		msgBus,
		false,
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "error reloading agent loop") {
		t.Fatalf("executeReload() error = %v, want agent reload failure", err)
	}
	if got := newProvider.closes.Load(); got != 1 {
		t.Fatalf("new provider Close() calls = %d, want 1", got)
	}
	if providerRef != oldProvider {
		t.Fatal("provider reference changed after agent reload failure")
	}
	if runningServices.HeartbeatService == nil || !runningServices.HeartbeatService.IsRunning() {
		t.Fatal("heartbeat service was not restored after agent reload failure")
	}
}

func TestShutdownGatewayClosesProviderAndStopsServices(t *testing.T) {
	_, al, msgBus, runningServices := newLifecycleFixture(t)
	provider := &lifecycleTestProvider{reason: "provider"}

	shutdownGateway(runningServices, al, provider, msgBus, true)

	if got := provider.closes.Load(); got != 1 {
		t.Fatalf("provider Close() calls = %d, want 1", got)
	}
	if runningServices.HeartbeatService.IsRunning() {
		t.Fatal("heartbeat service still running after shutdown")
	}
	if err := msgBus.PublishVoiceControl(context.Background(), bus.VoiceControl{}); !errors.Is(err, bus.ErrBusClosed) {
		t.Fatalf("PublishVoiceControl() after shutdown error = %v, want ErrBusClosed", err)
	}
}
