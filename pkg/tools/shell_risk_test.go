package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func riskTestTool(t *testing.T, exec config.ExecConfig) *ExecTool {
	t.Helper()
	cfg := &config.Config{Tools: config.ToolsConfig{Exec: exec}}
	cfg.Tools.Exec.EnableDenyPatterns = true
	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig: %v", err)
	}
	return tool
}

func TestShellTool_RiskClassifierBlocksDefault(t *testing.T) {
	t.Parallel()

	tool := riskTestTool(t, config.ExecConfig{})

	// chmod +x does not match the legacy regex deny list (which only covers
	// numeric modes), so this assertion exercises the AST classifier.
	if got := tool.guardCommand("chmod +x script.sh", t.TempDir()); !strings.Contains(got, "risk classifier") {
		t.Fatalf("chmod +x should be blocked by the risk classifier, got %q", got)
	}
	if got := tool.guardCommand("x=rm; $x -rf /", t.TempDir()); !strings.Contains(got, "dynamic command name") {
		t.Fatalf("variable indirection should be blocked, got %q", got)
	}
	if got := tool.guardCommand("ls -la", t.TempDir()); got != "" {
		t.Fatalf("safe command should pass, got %q", got)
	}
}

func TestShellTool_RiskClassifierExecute(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := riskTestTool(t, config.ExecConfig{})
	ctx := WithToolContext(context.Background(), "cli", "test")

	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "chmod +x " + filepath.Join(dir, "script.sh"),
	})
	if !result.IsError || !strings.Contains(result.ForLLM, "risk classifier") {
		t.Fatalf("execute should return the risk error, got: %s", result.ForLLM)
	}

	result = tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "x=rm; $x -rf /",
	})
	if !result.IsError || !strings.Contains(result.ForLLM, "dynamic command name") {
		t.Fatalf("variable indirection should be rejected, got: %s", result.ForLLM)
	}

	allowed := riskTestTool(t, config.ExecConfig{RiskThreshold: "high"})
	script := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	result = allowed.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "chmod +x " + script,
	})
	if result.IsError {
		t.Fatalf("high threshold should allow chmod, got: %s", result.ForLLM)
	}
}

func TestShellTool_RiskCalibration(t *testing.T) {
	t.Parallel()

	tool := riskTestTool(t, config.ExecConfig{})
	cwd := t.TempDir()

	allowed := []string{
		"docker ps",
		"docker logs app",
		"docker inspect app",
		"systemctl status nginx",
		"kubectl get pods",
		"kubectl describe pod x",
		"git status",
		"git log --oneline",
		"git diff",
		"rm file",
		"terraform plan",
		"helm list",
		"aws s3 ls",
		"cryptsetup status /dev/sda",
		"zpool status",
		"mdadm --detail /dev/md0",
	}
	for _, command := range allowed {
		if got := tool.guardCommand(command, cwd); got != "" {
			t.Errorf("command should pass the guard: %s\n  got: %s", command, got)
		}
	}

	blocked := []string{
		"docker run image",
		"systemctl start nginx",
		"kubectl delete pod x",
		"git branch -D feature",
		"rm -r build",
		"rm -rf build",
		"wipefs /dev/sda",
		"blkdiscard /dev/sda",
		"lvremove vg/lv",
		"cryptsetup luksFormat /dev/sda",
		"zpool destroy tank",
		"mdadm --zero-superblock /dev/sda",
		"terraform destroy",
		"helm uninstall release",
		"aws s3 rm s3://bucket/key",
	}
	for _, command := range blocked {
		if got := tool.guardCommand(command, cwd); got == "" {
			t.Errorf("command should be blocked: %s", command)
		}
	}
}

func TestShellTool_RiskRmCalibration(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	medium := riskTestTool(t, config.ExecConfig{})
	if got := medium.guardCommand("rm file", dir); got != "" {
		t.Fatalf("plain rm should be allowed at medium, got %q", got)
	}
	if got := medium.guardCommand("rm -r build", dir); !strings.Contains(got, "risk classifier") {
		t.Fatalf("rm -r should be blocked by the risk classifier at medium, got %q", got)
	}
	if got := medium.guardCommand("rm -rf build", dir); !strings.Contains(got, "risk classifier") {
		t.Fatalf("rm -rf should be blocked by the risk classifier at medium, got %q", got)
	}

	high := riskTestTool(t, config.ExecConfig{RiskThreshold: "high"})
	if got := high.guardCommand("rm -r build", dir); got != "" {
		t.Fatalf("rm -r should be allowed at high, got %q", got)
	}
	if got := high.guardCommand("rm -rf build", dir); !strings.Contains(got, "risk classifier") {
		t.Fatalf("rm -rf should stay blocked at high, got %q", got)
	}
}

func TestShellTool_RiskThresholdCanBeRaised(t *testing.T) {
	t.Parallel()

	tool := riskTestTool(t, config.ExecConfig{RiskThreshold: "high"})
	if got := tool.guardCommand("chmod +x script.sh", t.TempDir()); got != "" {
		t.Fatalf("high threshold should allow chmod, got %q", got)
	}

	critical := riskTestTool(t, config.ExecConfig{RiskThreshold: "critical"})
	if got := critical.guardCommand("chmod +x script.sh", t.TempDir()); got != "" {
		t.Fatalf("critical threshold should allow chmod, got %q", got)
	}
}

func TestShellTool_RiskOverrides(t *testing.T) {
	t.Parallel()

	tool := riskTestTool(t, config.ExecConfig{
		RiskThreshold: "medium",
		RiskOverrides: map[string]string{"chmod": "low"},
	})
	if got := tool.guardCommand("chmod +x script.sh", t.TempDir()); got != "" {
		t.Fatalf("override should allow chmod, got %q", got)
	}
}

func TestShellTool_UserArgModifiers(t *testing.T) {
	t.Parallel()

	tool := riskTestTool(t, config.ExecConfig{
		RiskThreshold: "medium",
		ArgModifiers: map[string][]config.ArgModifierConfig{
			"echo": {{Args: []string{"--danger"}, Level: "high"}},
		},
	})
	if got := tool.guardCommand("echo --danger", t.TempDir()); !strings.Contains(got, "risk classifier") {
		t.Fatalf("user modifier should block echo --danger, got %q", got)
	}
	if got := tool.guardCommand("echo safe", t.TempDir()); got != "" {
		t.Fatalf("unmodified echo should pass, got %q", got)
	}
}

func TestShellTool_CustomAllowPatternExemptsRisk(t *testing.T) {
	t.Parallel()

	tool := riskTestTool(t, config.ExecConfig{
		RiskThreshold:       "medium",
		CustomAllowPatterns: []string{`^chmod \+x\b`},
	})
	if got := tool.guardCommand("chmod +x script.sh", t.TempDir()); got != "" {
		t.Fatalf("custom allow pattern should exempt the risk classifier, got %q", got)
	}
}

func TestShellTool_RiskDisabledWithDefaultDenyPatterns(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Tools: config.ToolsConfig{
			Exec: config.ExecConfig{EnableDenyPatterns: false},
		},
	}
	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig: %v", err)
	}
	if got := tool.guardCommand("chmod +x script.sh", t.TempDir()); got != "" {
		t.Fatalf("risk classifier must be disabled with default deny patterns, got %q", got)
	}
}

func TestShellTool_InvalidRiskConfigRejected(t *testing.T) {
	t.Parallel()

	tests := []config.ExecConfig{
		{RiskThreshold: "bogus"},
		{RiskThreshold: "medium", RiskOverrides: map[string]string{"chmod": "bogus"}},
		{
			RiskThreshold: "medium",
			ArgModifiers: map[string][]config.ArgModifierConfig{
				"echo": {{Args: []string{"--danger"}, Level: "bogus"}},
			},
		},
		{
			RiskThreshold: "medium",
			ArgModifiers: map[string][]config.ArgModifierConfig{
				"echo": {{Args: []string{"--danger"}}},
			},
		},
	}
	for _, exec := range tests {
		cfg := &config.Config{Tools: config.ToolsConfig{Exec: exec}}
		cfg.Tools.Exec.EnableDenyPatterns = true
		if _, err := NewExecToolWithConfig(t.TempDir(), false, cfg); err == nil {
			t.Fatalf("invalid risk config should be rejected: %+v", exec)
		}
	}
}
