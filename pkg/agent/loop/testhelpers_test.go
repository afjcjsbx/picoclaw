package loop

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	agenttools "github.com/sipeed/picoclaw/pkg/tools"
)

func roles(msgs []providers.Message) []string {
	r := make([]string, len(msgs))
	for i, m := range msgs {
		r[i] = m.Role
	}
	return r
}

func assertRoles(t *testing.T, msgs []providers.Message, expected ...string) {
	t.Helper()
	if len(msgs) != len(expected) {
		t.Fatalf("role count mismatch: got %v, want %v", roles(msgs), expected)
	}
	for i, exp := range expected {
		if msgs[i].Role != exp {
			t.Errorf("message[%d]: got role %q, want %q", i, msgs[i].Role, exp)
		}
	}
}

// setupWorkspace creates a temporary workspace with standard directories and optional files.
// Returns the tmpDir path; caller should defer os.RemoveAll(tmpDir).
func setupWorkspace(t *testing.T, files map[string]string) string {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "picoclaw-test-*")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(tmpDir, "memory"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "skills"), 0o755)
	for name, content := range files {
		dir := filepath.Dir(filepath.Join(tmpDir, name))
		os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return tmpDir
}

func cleanupWorkspace(t *testing.T, path string) {
	t.Helper()
	if err := os.RemoveAll(path); err != nil {
		t.Fatalf("failed to clean up workspace %s: %v", path, err)
	}
}

type mockRegistryProvider struct{}

func (m *mockRegistryProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	options map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "mock", FinishReason: "stop"}, nil
}

func (m *mockRegistryProvider) GetDefaultModel() string {
	return "mock-model"
}

func testCfg(agents []config.AgentConfig) *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         "/tmp/picoclaw-test-registry",
				ModelName:         "gpt-4",
				MaxTokens:         8192,
				MaxToolIterations: 10,
			},
			List: agents,
		},
	}
}

type allowlistTestTool struct {
	name string
}

func (t *allowlistTestTool) Name() string { return t.name }

func (t *allowlistTestTool) Description() string { return "test tool" }

func (t *allowlistTestTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}

func (t *allowlistTestTool) Execute(
	_ context.Context,
	_ map[string]any,
) *agenttools.ToolResult {
	return agenttools.NewToolResult("ok")
}
