package tools

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
)

func newPolicyTestTool(t *testing.T, exec config.ExecConfig) *ExecTool {
	t.Helper()
	cfg := &config.Config{Tools: config.ToolsConfig{Exec: exec}}
	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	require.NoError(t, err)
	return tool
}

func TestExecPolicyPatternsBlockDestructiveOperations(t *testing.T) {
	t.Parallel()

	tool := newPolicyTestTool(t, config.ExecConfig{EnableDenyPatterns: true})
	blocked := []string{
		"rm -rf build",
		"rm -r build",
		"git push --force origin main",
		"git reset --hard HEAD~1",
		"git clean -fdx",
		"terraform destroy",
		"kubectl delete pod x",
		"helm uninstall release",
		"aws s3 rm s3://bucket/key",
		"aws s3api delete-object --bucket b --key k",
		"npm publish",
		"docker rm container",
		"docker run --privileged image",
		"chmod -R 777 .",
		"chown -R user:user .",
		"find . -delete",
		"truncate -s 0 file",
		"shred file",
		"kill 12345",
	}
	for _, command := range blocked {
		if got := tool.guardCommandContext(command, t.TempDir()); got == "" {
			t.Errorf("policy should block %q", command)
		}
	}
}

func TestExecPolicyPatternsAllowOrdinaryCommands(t *testing.T) {
	t.Parallel()

	tool := newPolicyTestTool(t, config.ExecConfig{EnableDenyPatterns: true})
	allowed := []string{
		"rm file",
		"git status",
		"git push origin main",
		"terraform plan",
		"kubectl get pods",
		"aws s3 ls",
		"docker ps",
		"find . -name '*.go'",
		"chmod +x script.sh",
	}
	for _, command := range allowed {
		if got := tool.guardCommandContext(command, t.TempDir()); got != "" {
			t.Errorf("policy should allow %q, got %q", command, got)
		}
	}
}

func TestExecPolicyCustomAllowExemptsPolicy(t *testing.T) {
	t.Parallel()

	tool := newPolicyTestTool(t, config.ExecConfig{
		EnableDenyPatterns:  true,
		CustomAllowPatterns: []string{`rm -rf build`},
	})
	if got := tool.guardCommandContext("rm -rf build", t.TempDir()); got != "" {
		t.Fatalf("custom allow should exempt the exact command, got %q", got)
	}
	if got := tool.guardCommandContext("rm -rf dist", t.TempDir()); got == "" {
		t.Fatal("custom allow must stay anchored to the exact command")
	}
}

func TestExecPolicyDisabledStillKeepsHardline(t *testing.T) {
	t.Parallel()

	tool := newPolicyTestTool(t, config.ExecConfig{EnableDenyPatterns: false})
	if got := tool.guardCommandContext("rm -rf build", t.TempDir()); got != "" {
		t.Fatalf("policy must be disabled with enable_deny_patterns=false, got %q", got)
	}
	if got := tool.guardCommandContext("rm -rf /", t.TempDir()); got == "" {
		t.Fatal("hardline floor must stay active when policy patterns are disabled")
	}
}
