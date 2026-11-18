package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	ppid "github.com/sipeed/picoclaw/pkg/pid"
)

func TestCronProxyForwardsToGateway(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PICOCLAW_HOME", tmpDir)
	t.Setenv("HOME", tmpDir)

	var seen []string
	var patchBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer cron-token" {
			writeCronProxyError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if r.Method == http.MethodPatch {
			body, _ := io.ReadAll(r.Body)
			patchBody = string(body)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/internal/cron/jobs":
			_, _ = w.Write([]byte(`{"jobs":[{"id":"job-1","name":"Test","enabled":true}],"runtime_available":true}`))
		case "/internal/cron/jobs/job-1/run":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"status":"running"}`))
		case "/internal/cron/jobs/job-1":
			if r.Method == http.MethodDelete {
				_, _ = w.Write([]byte(`{"status":"deleted"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"job-1","enabled":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	parsed, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatalf("Atoi(port) error = %v", err)
	}

	configPath := filepath.Join(tmpDir, "config.json")
	cfg := config.DefaultConfig()
	cfg.Gateway.Host = parsed.Hostname()
	cfg.Gateway.Port = port
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	cmd := startGatewayLikeProcess(t)
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	origPidData := gateway.pidData
	origPicoToken := gateway.picoToken
	origCmd := gateway.cmd
	origStatus := gateway.runtimeStatus
	t.Cleanup(func() {
		gateway.mu.Lock()
		gateway.pidData = origPidData
		gateway.picoToken = origPicoToken
		gateway.cmd = origCmd
		gateway.runtimeStatus = origStatus
		gateway.mu.Unlock()
	})

	gateway.mu.Lock()
	gateway.pidData = &ppid.PidFileData{PID: cmd.Process.Pid, Token: "cron-token"}
	gateway.cmd = cmd
	setGatewayRuntimeStatusLocked("running")
	gateway.mu.Unlock()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerCronRoutes(mux)

	tests := []struct {
		method string
		path   string
		body   string
		status int
	}{
		{method: http.MethodGet, path: "/api/cron/jobs", status: http.StatusOK},
		{method: http.MethodPost, path: "/api/cron/jobs/job-1/run", status: http.StatusAccepted},
		{
			method: http.MethodPatch,
			path:   "/api/cron/jobs/job-1",
			body:   `{"enabled":false}`,
			status: http.StatusOK,
		},
		{method: http.MethodDelete, path: "/api/cron/jobs/job-1", status: http.StatusOK},
	}
	for _, tt := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
		mux.ServeHTTP(recorder, request)
		if recorder.Code != tt.status {
			t.Fatalf(
				"%s %s status = %d, want %d (body %s)",
				tt.method,
				tt.path,
				recorder.Code,
				tt.status,
				recorder.Body.String(),
			)
		}
	}

	wantSeen := []string{
		"GET /internal/cron/jobs",
		"POST /internal/cron/jobs/job-1/run",
		"PATCH /internal/cron/jobs/job-1",
		"DELETE /internal/cron/jobs/job-1",
	}
	if len(seen) != len(wantSeen) {
		t.Fatalf("seen = %v, want %v", seen, wantSeen)
	}
	for i := range wantSeen {
		if seen[i] != wantSeen[i] {
			t.Fatalf("seen[%d] = %q, want %q", i, seen[i], wantSeen[i])
		}
	}
	if patchBody != `{"enabled":false}` {
		t.Fatalf("patch body = %q", patchBody)
	}
}

func TestCronProxyReturnsUnavailableWithoutGateway(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("PICOCLAW_HOME", tmpDir)
	t.Setenv("HOME", tmpDir)

	origPidData := gateway.pidData
	origCmd := gateway.cmd
	origStatus := gateway.runtimeStatus
	t.Cleanup(func() {
		gateway.mu.Lock()
		gateway.pidData = origPidData
		gateway.cmd = origCmd
		gateway.runtimeStatus = origStatus
		gateway.mu.Unlock()
	})

	gateway.mu.Lock()
	gateway.pidData = nil
	gateway.cmd = nil
	gateway.runtimeStatus = "stopped"
	gateway.mu.Unlock()

	h := NewHandler(filepath.Join(tmpDir, "config.json"))
	mux := http.NewServeMux()
	h.registerCronRoutes(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/cron/jobs", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "gateway is not running") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}
