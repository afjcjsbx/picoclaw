package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/isolation"
)

// TestExecToolDockerIsolationIntegration exercises the exec tool end to end
// with isolation.backend=docker. It is opt-in because it needs a Docker
// daemon and a pullable image.
func TestExecToolDockerIsolationIntegration(t *testing.T) {
	if os.Getenv("PICOCLAW_DOCKER_TEST") == "" {
		t.Skip("set PICOCLAW_DOCKER_TEST=1 to run the Docker isolation integration test")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI not available: %v", err)
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}
	image := strings.TrimSpace(os.Getenv("PICOCLAW_DOCKER_TEST_IMAGE"))
	if image == "" {
		image = "alpine:3.20"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}
	root, err := os.MkdirTemp(home, ".picoclaw-docker-exec-test-")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv(config.EnvHome, root)

	workspace := filepath.Join(root, "workspace")
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.RestrictToWorkspace = true
	cfg.Approvals.Mode = config.ApprovalModeOff
	cfg.Tools.Exec.AllowRemote = true
	cfg.Isolation = config.IsolationConfig{
		Enabled: true,
		Backend: config.IsolationBackendDocker,
		Docker: config.DockerIsolationConfig{
			Image: image,
		},
	}
	isolation.Configure(cfg)
	t.Cleanup(func() { isolation.Configure(config.DefaultConfig()) })

	tool, err := NewExecToolWithConfig(workspace, true, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig() error = %v", err)
	}
	ctx := context.Background()
	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "echo docker-exec-ok && touch marker.txt",
	})
	if result.IsError {
		t.Fatalf("exec failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "docker-exec-ok") {
		t.Fatalf("exec output missing marker: %s", result.ForLLM)
	}
	if _, err := os.Stat(filepath.Join(workspace, "marker.txt")); err != nil {
		t.Fatalf("container did not write the mounted workspace: %v", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	timedOut := tool.Execute(timeoutCtx, map[string]any{
		"action":  "run",
		"command": "sleep 30",
		"timeout": 2,
	})
	if !timedOut.IsError {
		t.Fatal("expected the timed-out command to be an error")
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err := exec.Command(
			"docker", "ps", "-a", "--filter", "name=picoclaw-", "--format", "{{.Names}}",
		).Output()
		if err != nil {
			t.Fatalf("docker ps error = %v", err)
		}
		if strings.TrimSpace(string(out)) == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("containers were not removed after timeout: %s", strings.TrimSpace(string(out)))
		}
		time.Sleep(200 * time.Millisecond)
	}
}
