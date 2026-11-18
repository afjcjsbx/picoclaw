package isolation

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

// TestDockerIsolationIntegration starts a real container. It is opt-in because
// it needs a Docker daemon, a pullable image and a host path shared with the
// Docker VM (on macOS the temp directory must live under the user home).
//
//	PICOCLAW_DOCKER_TEST=1 go test ./pkg/isolation -run TestDockerIsolationIntegration -v
func TestDockerIsolationIntegration(t *testing.T) {
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
	root, err := os.MkdirTemp(home, ".picoclaw-docker-test-")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv(config.EnvHome, root)

	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "input.txt"), []byte("exposed-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Isolation = config.IsolationConfig{
		Enabled: true,
		Backend: config.IsolationBackendDocker,
		ExposePaths: []config.ExposePath{
			{Source: filepath.Join(root, "workspace"), Target: filepath.Join(root, "workspace"), Mode: "rw"},
			{Source: source, Target: target, Mode: "rw"},
		},
		Docker: config.DockerIsolationConfig{
			Image: image,
		},
	}
	Configure(cfg)
	t.Cleanup(func() { Configure(config.DefaultConfig()) })

	if err := PrepareInstanceRoot(root); err != nil {
		t.Fatalf("PrepareInstanceRoot() error = %v", err)
	}
	if err := Preflight(); err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}

	workspace := filepath.Join(root, "workspace")
	cmd := exec.Command(
		"sh",
		"-c",
		"echo docker-isolation-ok; id -u; touch marker.txt; cat "+target+"/input.txt; echo written > "+target+"/out.txt",
	)
	cmd.Dir = workspace
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := Run(cmd); err != nil {
		t.Fatalf("Run() error = %v\noutput:\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "docker-isolation-ok") {
		t.Fatalf("output does not contain marker: %q", output.String())
	}
	if _, err := os.Stat(filepath.Join(workspace, "marker.txt")); err != nil {
		t.Fatalf("container did not write to the mounted workspace: %v", err)
	}
	if !strings.Contains(output.String(), "exposed-data") {
		t.Fatalf("container did not see the remapped expose_path: %q", output.String())
	}
	if data, err := os.ReadFile(filepath.Join(source, "out.txt")); err != nil {
		t.Fatalf("container did not write through the remapped expose_path: %v", err)
	} else if strings.TrimSpace(string(data)) != "written" {
		t.Fatalf("remapped write = %q, want written", string(data))
	}

	waitForNoContainers(t, 10*time.Second)

	// Killing a foreground command must also remove its container, because
	// killing the docker CLI does not stop the container it started.
	slow := exec.Command("sh", "-c", "sleep 30")
	slow.Dir = workspace
	slow.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	slow.Stdout = &output
	slow.Stderr = &output
	if err := Start(slow); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer Release(slow)

	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err := exec.Command("docker", "ps", "-q", "--filter", "label="+dockerContainerLabel).Output()
		if err != nil {
			t.Fatalf("docker ps error = %v", err)
		}
		if strings.TrimSpace(string(out)) != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slow container did not start")
		}
		time.Sleep(100 * time.Millisecond)
	}

	CleanupPID(slow.Process.Pid)
	_ = slow.Wait()
	waitForNoContainers(t, 10*time.Second)
}

func waitForNoContainers(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
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
			t.Fatalf("containers were not removed: %s", strings.TrimSpace(string(out)))
		}
		time.Sleep(200 * time.Millisecond)
	}
}
