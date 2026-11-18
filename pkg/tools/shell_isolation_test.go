package tools

import (
	"runtime"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestExecIsolationUsesPerToolOverride(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Isolation = config.IsolationConfig{Mode: config.IsolationModeRequired}
	cfg.Tools.Exec.Isolation = &config.IsolationConfig{Mode: config.IsolationModeOff}

	tool, err := NewExecToolWithConfig(t.TempDir(), true, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig: %v", err)
	}
	if tool.sandboxActive {
		t.Fatal("per-tool isolation=off must override the required global mode")
	}
	if got := tool.isolationConfig.Mode; got != config.IsolationModeOff {
		t.Fatalf("isolation mode = %q, want off", got)
	}
}

func TestExecIsolationInheritsGlobalMode(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Isolation = config.IsolationConfig{Mode: config.IsolationModeRequired}

	tool, err := NewExecToolWithConfig(t.TempDir(), true, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig: %v", err)
	}
	if !tool.sandboxActive {
		t.Fatal("required global isolation must activate the exec sandbox")
	}
	if got := tool.isolationConfig.Mode; got != config.IsolationModeRequired {
		t.Fatalf("isolation mode = %q, want required", got)
	}
}

func TestExecIsolationRejectsUnknownMode(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Tools.Exec.Isolation = &config.IsolationConfig{Mode: "yolo"}

	if _, err := NewExecToolWithConfig(t.TempDir(), true, cfg); err == nil {
		t.Fatal("unknown per-tool isolation mode must be rejected")
	}
}

func TestExecSandboxSkipsStaticWorkspaceAndInterpreterPass(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Tools.Exec.Isolation = &config.IsolationConfig{Mode: config.IsolationModeRequired}
	tool, err := NewExecToolWithConfig(workspace, true, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig: %v", err)
	}
	if !tool.sandboxActive {
		t.Fatal("required isolation should mark the sandbox active")
	}

	// The kernel owns path containment, so the static workspace/interpreter
	// pass must not reject these commands. Risk and hardline still run.
	for _, command := range []string{
		"cat /etc/passwd",
		`python3 -c "open('/tmp/canary','w').write('x')"`,
	} {
		if got := tool.guardCommand(command, workspace); got != "" {
			t.Errorf("sandboxed command %q should skip the static path pass, got %q", command, got)
		}
	}
	if got := tool.guardCommand("rm -rf /", workspace); !strings.Contains(got, "safety guard") {
		t.Fatalf("hardline must still run with the sandbox active, got %q", got)
	}
}

func TestShellCommandAppliesResourceLimits(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("POSIX resource limits are not applied on Windows")
	}
	path, args, err := shellCommand("echo hi")
	if err != nil {
		t.Fatalf("shellCommand: %v", err)
	}
	if path == "" || len(args) != 2 {
		t.Fatalf("shellCommand returned path=%q args=%v", path, args)
	}
	for _, want := range []string{"ulimit -n 4096", "ulimit -f 2097152", "ulimit -u 4096"} {
		if !strings.Contains(args[1], want) {
			t.Errorf("shell command missing %q: %q", want, args[1])
		}
	}
	if !strings.Contains(args[1], "echo hi") {
		t.Errorf("shell command lost the user command: %q", args[1])
	}
}
