package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/health"
	ppid "github.com/sipeed/picoclaw/pkg/pid"
)

type fakeGatewayReloadServer struct {
	mu              sync.Mutex
	token           string
	generation      uint64
	inProgress      bool
	lastError       string
	postError       string
	startedAt       *time.Time
	finishedAt      *time.Time
	postCalls       int
	rejectFirstPost bool
	disableStatus   bool
	authHeaders     []string
}

func (f *fakeGatewayReloadServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.authHeaders = append(f.authHeaders, r.Header.Get("Authorization"))
	if f.token != "" && r.Header.Get("Authorization") != "Bearer "+f.token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch r.URL.Path {
	case "/reload/status":
		if f.disableStatus {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(health.ReloadStatus{
			Generation: f.generation,
			InProgress: f.inProgress,
			LastError:  f.lastError,
			StartedAt:  f.startedAt,
			FinishedAt: f.finishedAt,
		})
	case "/reload":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		f.postCalls++
		if f.rejectFirstPost {
			f.rejectFirstPost = false
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"reload already in progress"}`))
			return
		}
		f.generation++
		f.inProgress = false
		f.lastError = f.postError
		now := time.Now()
		f.startedAt = &now
		f.finishedAt = &now
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"reload scheduled"}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeGatewayReloadServer) setLastError(err string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.postError = err
}

func newFakeGatewayReloadServer(t *testing.T, token string) (*fakeGatewayReloadServer, *httptest.Server) {
	t.Helper()

	fake := &fakeGatewayReloadServer{token: token}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return fake, server
}

func attachFakeGateway(t *testing.T, server *httptest.Server, token string) {
	t.Helper()

	cmd := startGatewayLikeProcess(t)
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse fake gateway URL: %v", err)
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatalf("split fake gateway host: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse fake gateway port: %v", err)
	}

	gateway.mu.Lock()
	gateway.cmd = cmd
	gateway.pidData = &ppid.PidFileData{
		PID:   cmd.Process.Pid,
		Token: token,
		Host:  host,
		Port:  port,
	}
	setGatewayRuntimeStatusLocked("running")
	gateway.mu.Unlock()
}

func TestReloadGatewayAfterConfigSaveUpdatesBootSignature(t *testing.T) {
	resetGatewayTestState(t)

	fake, server := newFakeGatewayReloadServer(t, "gateway-token")
	attachFakeGateway(t, server, "gateway-token")

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.ModelName = cfg.ModelList[0].ModelName

	gateway.mu.Lock()
	gateway.bootConfigSignature = "stale-signature"
	gateway.bootDefaultModel = "stale-model"
	gateway.mu.Unlock()

	h := NewHandler(filepath.Join(t.TempDir(), "config.json"))
	if err := h.reloadGatewayAfterConfigSave(cfg); err != nil {
		t.Fatalf("reloadGatewayAfterConfigSave() error = %v", err)
	}

	fake.mu.Lock()
	postCalls := fake.postCalls
	authHeaders := append([]string(nil), fake.authHeaders...)
	fake.mu.Unlock()

	if postCalls != 1 {
		t.Fatalf("POST /reload calls = %d, want 1", postCalls)
	}
	for _, header := range authHeaders {
		if header != "Bearer gateway-token" {
			t.Fatalf("Authorization header = %q, want bearer token", header)
		}
	}

	gateway.mu.Lock()
	bootSignature := gateway.bootConfigSignature
	bootModel := gateway.bootDefaultModel
	gateway.mu.Unlock()

	if want := computeConfigSignature(cfg); bootSignature != want {
		t.Fatalf("boot config signature = %q, want %q", bootSignature, want)
	}
	if bootModel != cfg.Agents.Defaults.ModelName {
		t.Fatalf("boot default model = %q, want %q", bootModel, cfg.Agents.Defaults.ModelName)
	}
}

func TestReloadGatewayAfterConfigSaveReportsReloadFailure(t *testing.T) {
	resetGatewayTestState(t)

	fake, server := newFakeGatewayReloadServer(t, "gateway-token")
	fake.setLastError("provider creation failed")
	attachFakeGateway(t, server, "gateway-token")

	cfg := config.DefaultConfig()
	gateway.mu.Lock()
	gateway.bootConfigSignature = "stale-signature"
	gateway.mu.Unlock()

	h := NewHandler(filepath.Join(t.TempDir(), "config.json"))
	err := h.reloadGatewayAfterConfigSave(cfg)
	if err == nil {
		t.Fatal("reloadGatewayAfterConfigSave() = nil, want reload failure")
	}
	if !strings.Contains(err.Error(), "provider creation failed") {
		t.Fatalf("error = %v, want provider creation failure", err)
	}

	gateway.mu.Lock()
	bootSignature := gateway.bootConfigSignature
	gateway.mu.Unlock()
	if bootSignature != "stale-signature" {
		t.Fatalf("boot config signature = %q, want unchanged stale signature", bootSignature)
	}
}

func TestReloadGatewayAfterConfigSaveSkipsStoppedGateway(t *testing.T) {
	resetGatewayTestState(t)

	fake, _ := newFakeGatewayReloadServer(t, "gateway-token")

	cfg := config.DefaultConfig()
	h := NewHandler(filepath.Join(t.TempDir(), "config.json"))
	if err := h.reloadGatewayAfterConfigSave(cfg); err != nil {
		t.Fatalf("reloadGatewayAfterConfigSave() error = %v", err)
	}

	fake.mu.Lock()
	postCalls := fake.postCalls
	fake.mu.Unlock()
	if postCalls != 0 {
		t.Fatalf("POST /reload calls = %d, want 0 for stopped gateway", postCalls)
	}
}

func TestReloadGatewayAfterConfigSaveTriggersReloadWhenStatusUnavailable(t *testing.T) {
	resetGatewayTestState(t)

	fake, server := newFakeGatewayReloadServer(t, "gateway-token")
	fake.mu.Lock()
	fake.disableStatus = true
	fake.mu.Unlock()
	attachFakeGateway(t, server, "gateway-token")

	cfg := config.DefaultConfig()
	h := NewHandler(filepath.Join(t.TempDir(), "config.json"))
	err := h.reloadGatewayAfterConfigSave(cfg)
	if err == nil {
		t.Fatal("reloadGatewayAfterConfigSave() = nil, want status unavailable error")
	}
	if !strings.Contains(err.Error(), "status") {
		t.Fatalf("error = %v, want status error", err)
	}

	fake.mu.Lock()
	postCalls := fake.postCalls
	fake.mu.Unlock()
	if postCalls != 1 {
		t.Fatalf("POST /reload calls = %d, want 1 fallback trigger", postCalls)
	}
}

func TestReloadGatewayAfterConfigSaveRetriesWhenReloadAlreadyInProgress(t *testing.T) {
	resetGatewayTestState(t)

	fake, server := newFakeGatewayReloadServer(t, "gateway-token")
	fake.mu.Lock()
	fake.rejectFirstPost = true
	fake.mu.Unlock()
	attachFakeGateway(t, server, "gateway-token")

	cfg := config.DefaultConfig()
	h := NewHandler(filepath.Join(t.TempDir(), "config.json"))
	if err := h.reloadGatewayAfterConfigSave(cfg); err != nil {
		t.Fatalf("reloadGatewayAfterConfigSave() error = %v", err)
	}

	fake.mu.Lock()
	postCalls := fake.postCalls
	fake.mu.Unlock()
	if postCalls != 2 {
		t.Fatalf("POST /reload calls = %d, want 2 after retry", postCalls)
	}
}

func TestHandleSetDefaultModelReloadsRunningGateway(t *testing.T) {
	resetGatewayTestState(t)

	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.DefaultConfig()
	cfg.ModelList = []*config.ModelConfig{
		openAIModelConfig("primary"),
		openAIModelConfig("secondary"),
	}
	cfg.Agents.Defaults.ModelName = "primary"
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	fake, server := newFakeGatewayReloadServer(t, "gateway-token")
	attachFakeGateway(t, server, "gateway-token")

	gateway.mu.Lock()
	gateway.bootConfigSignature = computeConfigSignature(cfg)
	gateway.bootDefaultModel = "primary"
	gateway.mu.Unlock()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/models/default",
		strings.NewReader(`{"model_name":"secondary"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	fake.mu.Lock()
	postCalls := fake.postCalls
	fake.mu.Unlock()
	if postCalls != 1 {
		t.Fatalf("POST /reload calls = %d, want 1", postCalls)
	}

	updatedCfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	gateway.mu.Lock()
	bootSignature := gateway.bootConfigSignature
	bootModel := gateway.bootDefaultModel
	gateway.mu.Unlock()

	if want := computeConfigSignature(updatedCfg); bootSignature != want {
		t.Fatalf("boot config signature = %q, want %q", bootSignature, want)
	}
	if bootModel != "secondary" {
		t.Fatalf("boot default model = %q, want %q", bootModel, "secondary")
	}
}

func TestHandleSetDefaultModelKeepsSaveWhenGatewayReloadFails(t *testing.T) {
	resetGatewayTestState(t)

	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.DefaultConfig()
	cfg.ModelList = []*config.ModelConfig{
		openAIModelConfig("primary"),
		openAIModelConfig("secondary"),
	}
	cfg.Agents.Defaults.ModelName = "primary"
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	fake, server := newFakeGatewayReloadServer(t, "gateway-token")
	fake.setLastError("provider creation failed")
	attachFakeGateway(t, server, "gateway-token")

	gateway.mu.Lock()
	gateway.bootConfigSignature = computeConfigSignature(cfg)
	gateway.mu.Unlock()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/models/default",
		strings.NewReader(`{"model_name":"secondary"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	updatedCfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if updatedCfg.Agents.Defaults.ModelName != "secondary" {
		t.Fatalf(
			"persisted default model = %q, want %q",
			updatedCfg.Agents.Defaults.ModelName,
			"secondary",
		)
	}

	gateway.mu.Lock()
	bootSignature := gateway.bootConfigSignature
	gateway.mu.Unlock()
	if want := computeConfigSignature(cfg); bootSignature != want {
		t.Fatalf("boot config signature = %q, want unchanged %q", bootSignature, want)
	}
}

func TestReloadGatewayAfterConfigSaveWaitTimeout(t *testing.T) {
	resetGatewayTestState(t)

	fake, server := newFakeGatewayReloadServer(t, "gateway-token")
	fake.mu.Lock()
	fake.inProgress = true
	fake.mu.Unlock()
	attachFakeGateway(t, server, "gateway-token")

	originalWait := gatewayReloadWaitTimeout
	originalPoll := gatewayReloadPollInterval
	gatewayReloadWaitTimeout = 50 * time.Millisecond
	gatewayReloadPollInterval = 5 * time.Millisecond
	t.Cleanup(func() {
		gatewayReloadWaitTimeout = originalWait
		gatewayReloadPollInterval = originalPoll
	})

	cfg := config.DefaultConfig()
	h := NewHandler(filepath.Join(t.TempDir(), "config.json"))
	err := h.reloadGatewayAfterConfigSave(cfg)
	if err == nil {
		t.Fatal("reloadGatewayAfterConfigSave() = nil, want timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want timeout", err)
	}
}
