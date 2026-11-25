package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func riskTestTool(t *testing.T, exec config.ExecConfig) *ExecTool {
	t.Helper()
	cfg := &config.Config{
		Tools: config.ToolsConfig{Exec: exec},
	}
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
	if got := tool.guardCommand("rm -r build", t.TempDir()); !strings.Contains(got, "risk classifier") {
		t.Fatalf("known high-risk command should be blocked, got %q", got)
	}
	if got := tool.guardCommand("ls -la", t.TempDir()); got != "" {
		t.Fatalf("safe command should pass, got %q", got)
	}
}

func TestShellTool_RiskNestedLaunchers(t *testing.T) {
	t.Parallel()

	tool := riskTestTool(t, config.ExecConfig{})
	blocked := []string{
		"command rm -rf -v build",
		"command chmod -v 777 script.sh",
		"command ssh -v host",
		"busybox sh -c 'rm -rf build'",
		"busybox rm -rf build",
	}
	for _, command := range blocked {
		if got := tool.guardCommand(command, t.TempDir()); !strings.Contains(got, "risk classifier") {
			t.Errorf("command %q should be blocked by the risk classifier, got %q", command, got)
		}
	}
	for _, command := range []string{"command -v rm", "command -pv rm", "busybox ls -la"} {
		if got := tool.guardCommand(command, t.TempDir()); got != "" {
			t.Errorf("command %q should be allowed, got %q", command, got)
		}
	}

	high := riskTestTool(t, config.ExecConfig{RiskThreshold: "high"})
	for _, command := range []string{
		`find . -exec rm -rf build \;`,
		`find . -execdir rm -rf build +`,
	} {
		if got := high.guardCommand(command, t.TempDir()); !strings.Contains(got, "risk classifier") {
			t.Errorf("command %q should block its nested command, got %q", command, got)
		}
	}
}

func TestShellTool_UnknownCommandsAllowedByDefault(t *testing.T) {
	t.Parallel()

	tool := riskTestTool(t, config.ExecConfig{})
	allowed := []string{
		"sh script.sh",
		"bash scripts/deploy.sh",
		"x=echo; $x ok",
		"xargs sh -c 'echo ok'",
		`trap "$ACTION" EXIT`,
		"env -S 'echo ok'",
		"echo )",
	}
	for _, command := range allowed {
		if got := tool.guardCommand(command, t.TempDir()); got != "" {
			t.Errorf("unknown command %q should be allowed by default, got %q", command, got)
		}
	}
}

func TestShellTool_ParseFailureAllowedByRiskGuard(t *testing.T) {
	t.Parallel()

	unrestricted := riskTestTool(t, config.ExecConfig{})
	if got := unrestricted.guardCommand("echo )", t.TempDir()); got != "" {
		t.Fatalf("risk guard should allow an unclassifiable command, got %q", got)
	}

	restricted, err := NewExecToolWithConfig(t.TempDir(), true, nil)
	if err != nil {
		t.Fatalf("NewExecTool: %v", err)
	}
	if got := restricted.guardCommand("echo foo)", t.TempDir()); !strings.Contains(got, "could not be parsed") {
		t.Fatalf("workspace guard should reject a parse failure, got %q", got)
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
		"command": "x=echo; $x ok",
	})
	if result.IsError {
		t.Fatalf("variable indirection should be allowed as unknown, got: %s", result.ForLLM)
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

// TestShellTool_DynamicDestructiveOperandNotExecuted verifies that a dynamic
// path cannot hide an outside-workspace target from the static guards.
func TestShellTool_DynamicDestructiveOperandNotExecuted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	canary := filepath.Join(dir, "canary.txt")
	if err := os.WriteFile(canary, []byte("must survive\n"), 0o600); err != nil {
		t.Fatalf("write canary: %v", err)
	}

	tool := riskTestTool(t, config.ExecConfig{})
	ctx := WithToolContext(context.Background(), "cli", "test")
	for _, command := range []string{
		`P=` + canary + `; rm -f "$P"`,
		`P=` + canary + `; r\m -rf "$P"`,
		`P=` + canary + `; r\m -f "$P"`,
	} {
		result := tool.Execute(ctx, map[string]any{"action": "run", "command": command})
		if !result.IsError || !strings.Contains(result.ForLLM, "dynamic operand for destructive command") {
			t.Fatalf("execute should reject %q, got: %s", command, result.ForLLM)
		}
		if _, err := os.Stat(canary); err != nil {
			t.Fatalf("blocked command %q removed the canary: %v", command, err)
		}
	}
}

func TestShellTool_RiskCalibration(t *testing.T) {
	t.Parallel()

	tool := riskTestTool(t, config.ExecConfig{})
	cwd := t.TempDir()

	allowed := []string{
		"systemctl status nginx",
		"git status",
		"git log --oneline",
		"git diff",
		"rm file",
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
		"systemctl start nginx",
		"git branch -D feature",
		"rm -r build",
		"rm -rf build",
		"wipefs /dev/sda",
		"blkdiscard /dev/sda",
		"lvremove vg/lv",
		"cryptsetup luksFormat /dev/sda",
		"zpool destroy tank",
		"mdadm --zero-superblock /dev/sda",
		"aws s3 rm s3://bucket/key",
	}
	for _, command := range blocked {
		if got := tool.guardCommand(command, cwd); got == "" {
			t.Errorf("command should be blocked: %s", command)
		}
	}
}

func TestShellTool_UnlistedCommandsUseUnknownPolicy(t *testing.T) {
	t.Parallel()

	tool := riskTestTool(t, config.ExecConfig{})
	cwd := t.TempDir()

	// docker, kubectl, terraform and helm are not in the risk table. The
	// default unknown policy allows them; the caller decides whether to review
	// or block them.
	for _, command := range []string{
		"docker ps",
		"docker run image",
		"kubectl get pods",
		"kubectl delete pod x",
		"terraform plan",
		"terraform destroy",
		"helm list",
		"helm uninstall release",
	} {
		if got := tool.guardCommand(command, cwd); got != "" {
			t.Errorf("unlisted command should be allowed by the unknown policy: %s\n  got: %s", command, got)
		}
	}
}

func TestAssessExecCommandUnknownAndReject(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the exec risk classifier is Unix-only")
	}

	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	cfg.Tools.Exec.RiskThreshold = "medium"

	known, active := AssessExecCommand("ls -la", cfg)
	if !active {
		t.Fatal("classifier should be active")
	}
	if known.Unknown || known.Reject {
		t.Fatalf("ls should be known and accepted, got %+v", known)
	}

	unknown, active := AssessExecCommand("docker ps", cfg)
	if !active {
		t.Fatal("classifier should be active")
	}
	if !unknown.Unknown {
		t.Fatalf("docker ps should be unknown, got %+v", unknown)
	}

	reject, active := AssessExecCommand(`rm "$TARGET"`, cfg)
	if !active {
		t.Fatal("classifier should be active")
	}
	if !reject.Reject {
		t.Fatalf("dynamic rm operand should be rejected, got %+v", reject)
	}

	cfg.Tools.Exec.EnableDenyPatterns = false
	if _, active := AssessExecCommand("docker ps", cfg); active {
		t.Fatal("classifier should be inactive when enable_deny_patterns is false")
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
	dir := t.TempDir()
	if got := critical.guardCommand("rm -rf /", dir); !strings.Contains(got, "dangerous pattern") {
		t.Fatalf("critical threshold must not allow root deletion, got %q", got)
	}
	if got := critical.guardCommand("rm -rf build", dir); got != "" {
		t.Fatalf("critical threshold should keep non-root rm -rf allowed, got %q", got)
	}
}

func TestShellTool_RiskThresholdControlsGitPush(t *testing.T) {
	t.Parallel()

	medium := riskTestTool(t, config.ExecConfig{RiskThreshold: "medium"})
	if got := medium.guardCommand("git push origin main", t.TempDir()); !strings.Contains(got, "risk classifier") {
		t.Fatalf("medium threshold should block git push through the risk classifier, got %q", got)
	}

	high := riskTestTool(t, config.ExecConfig{RiskThreshold: "high"})
	if got := high.guardCommand("git push origin main", t.TempDir()); got != "" {
		t.Fatalf("high threshold should allow git push now that the regex layer does not block it, got %q", got)
	}
}

func TestShellTool_RiskNormalizesIFSObfuscation(t *testing.T) {
	t.Parallel()

	tool := riskTestTool(t, config.ExecConfig{RiskThreshold: "medium"})
	if got := tool.guardCommand("git${IFS}push origin main", t.TempDir()); !strings.Contains(got, "risk classifier") {
		t.Fatalf("normalized risk parse should block git${IFS}push, got %q", got)
	}
}

func TestShellTool_CustomAllowDoesNotExemptRisk(t *testing.T) {
	t.Parallel()

	tool := riskTestTool(t, config.ExecConfig{
		RiskThreshold:       "medium",
		CustomAllowPatterns: []string{`chmod \+x\b.*`},
	})
	if got := tool.guardCommand("chmod +x script.sh", t.TempDir()); !strings.Contains(got, "risk classifier") {
		t.Fatalf("custom allow must not exempt the risk classifier, got %q", got)
	}
}

func TestShellTool_CustomAllowPatternsAreAnchored(t *testing.T) {
	t.Parallel()

	tool := riskTestTool(t, config.ExecConfig{
		RiskThreshold:       "high",
		CustomAllowPatterns: []string{`echo`},
	})
	if got := tool.guardCommand("echo", t.TempDir()); got != "" {
		t.Fatalf("anchored custom allow should match the exact command, got %q", got)
	}
	if got := tool.guardCommand("echo $(date)", t.TempDir()); !strings.Contains(got, "dangerous pattern") {
		t.Fatalf("bare echo allow must not exempt command substitution, got %q", got)
	}
}

func TestShellTool_BroadCustomAllowDoesNotExemptRisk(t *testing.T) {
	t.Parallel()

	tool := riskTestTool(t, config.ExecConfig{
		RiskThreshold:       "medium",
		CustomAllowPatterns: []string{`.*`},
	})
	if got := tool.guardCommand("git push origin main", t.TempDir()); !strings.Contains(got, "risk classifier") {
		t.Fatalf("broad custom allow must still leave the risk classifier active, got %q", got)
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
	}
	for _, exec := range tests {
		cfg := &config.Config{Tools: config.ToolsConfig{Exec: exec}}
		cfg.Tools.Exec.EnableDenyPatterns = true
		if _, err := NewExecToolWithConfig(t.TempDir(), false, cfg); err == nil {
			t.Fatalf("invalid risk config should be rejected: %+v", exec)
		}
	}
}

func TestAssessExecCommandDeniedByPatterns(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the exec risk classifier is Unix-only")
	}

	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	cfg.Tools.Exec.CustomDenyPatterns = []string{`docker\s+rm`}
	cfg.Tools.Exec.CustomAllowPatterns = []string{"docker run .*"}

	tests := []struct {
		name    string
		command string
		denied  bool
	}{
		{"unknown command stays askable", "docker ps", false},
		{"custom deny", "docker rm web", true},
		{"hardline", "docker ps; shutdown now", true},
		{"default deny", "docker ps | sh", true},
		{"default deny exempted by custom allow", "docker run `id`", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, active := AssessExecCommand(tt.command, cfg)
			if !active {
				t.Fatal("classifier should be active")
			}
			if !got.Unknown && !got.Reject {
				t.Fatalf("%q should be unknown or rejected, got %+v", tt.command, got)
			}
			if got.Denied != tt.denied && !got.Reject {
				t.Fatalf("%q Denied = %v, want %v (%+v)", tt.command, got.Denied, tt.denied, got)
			}
		})
	}
}
