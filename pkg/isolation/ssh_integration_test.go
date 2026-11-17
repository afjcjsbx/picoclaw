package isolation

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

// TestSSHIsolationIntegration runs commands on a real SSH host. It is opt-in
// because it needs a reachable host with key or agent authentication and a
// writable remote directory.
//
//	PICOCLAW_SSH_TEST=1 \
//	PICOCLAW_SSH_TEST_HOST=sandbox.internal \
//	PICOCLAW_SSH_TEST_USER=worker \
//	PICOCLAW_SSH_TEST_REMOTE_ROOT=/srv/picoclaw \
//	go test ./pkg/isolation -run TestSSHIsolationIntegration -v
func TestSSHIsolationIntegration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ssh backend is not supported on windows")
	}
	if os.Getenv("PICOCLAW_SSH_TEST") == "" {
		t.Skip("set PICOCLAW_SSH_TEST=1 to run the SSH isolation integration test")
	}
	host := strings.TrimSpace(os.Getenv("PICOCLAW_SSH_TEST_HOST"))
	if host == "" {
		t.Fatal("PICOCLAW_SSH_TEST_HOST is required")
	}
	remoteRoot := strings.TrimSpace(os.Getenv("PICOCLAW_SSH_TEST_REMOTE_ROOT"))
	if remoteRoot == "" {
		t.Fatal("PICOCLAW_SSH_TEST_REMOTE_ROOT is required")
	}
	port, err := strconv.Atoi(strings.TrimSpace(os.Getenv("PICOCLAW_SSH_TEST_PORT")))
	if err != nil {
		port = 22
	}
	sshCfg := config.SSHIsolationConfig{
		Host:       host,
		User:       strings.TrimSpace(os.Getenv("PICOCLAW_SSH_TEST_USER")),
		Port:       port,
		KeyPath:    strings.TrimSpace(os.Getenv("PICOCLAW_SSH_TEST_KEY")),
		RemoteRoot: remoteRoot,
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		t.Skipf("ssh client not available: %v", err)
	}

	root := t.TempDir()
	remoteBase := filepath.Join(remoteRoot, fmt.Sprintf("picoclaw-ssh-test-%d", time.Now().UnixNano()))
	sshCfg.RemoteRoot = remoteBase
	t.Cleanup(func() {
		args := buildSSHArgs(sshCfg, nil, "/bin/sh -c "+sshShellQuote("rm -rf "+sshShellQuote(remoteBase)), false)
		_ = exec.Command(sshPath, args[1:]...).Run()
	})

	remoteWorkspace := filepath.Join(remoteBase, "workspace")
	mkdir := "/bin/sh -c " + sshShellQuote("mkdir -p "+sshShellQuote(remoteWorkspace))
	if output, err := exec.Command(
		sshPath,
		buildSSHArgs(sshCfg, nil, mkdir, false)[1:]...,
	).CombinedOutput(); err != nil {
		t.Fatalf("prepare remote workspace: %v: %s", err, output)
	}

	t.Setenv(config.EnvHome, root)
	cfg := config.DefaultConfig()
	cfg.Isolation = config.IsolationConfig{
		Enabled: true,
		Backend: config.IsolationBackendSSH,
		SSH:     sshCfg,
	}
	Configure(cfg)
	t.Cleanup(func() { Configure(config.DefaultConfig()) })

	localWorkspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(localWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := PrepareInstanceRoot(root); err != nil {
		t.Fatalf("PrepareInstanceRoot() error = %v", err)
	}
	if err := Preflight(); err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}

	run := func(script string) string {
		cmd := exec.Command("/bin/sh", "-c", script)
		cmd.Dir = localWorkspace
		cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		if err := Run(cmd); err != nil {
			t.Fatalf("Run(%q) error = %v\noutput:\n%s", script, err, output.String())
		}
		return output.String()
	}

	if got := strings.TrimSpace(run("pwd")); got != remoteWorkspace {
		t.Fatalf("remote pwd = %q, want %q", got, remoteWorkspace)
	}
	run("touch marker.txt")
	if got := strings.TrimSpace(run("test -f marker.txt && echo present")); got != "present" {
		t.Fatalf("remote marker check = %q, want present", got)
	}
}
