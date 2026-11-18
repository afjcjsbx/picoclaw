package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// skillToolScriptProvider answers each Chat call with the next scripted tool
// call, then with "done", and keeps the last message list it was given.
type skillToolScriptProvider struct {
	script   []providers.ToolCall
	calls    int
	messages []providers.Message
}

func (p *skillToolScriptProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.messages = append([]providers.Message(nil), messages...)
	i := p.calls
	p.calls++
	if i < len(p.script) {
		tc := p.script[i]
		tc.ID = fmt.Sprintf("call_%d", i)
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{tc}, FinishReason: "tool_calls"}, nil
	}
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *skillToolScriptProvider) GetDefaultModel() string { return "test-model" }

func (p *skillToolScriptProvider) toolResults() []string {
	var out []string
	for _, m := range p.messages {
		if m.Role == "tool" {
			out = append(out, m.Content)
		}
	}
	return out
}

func TestSkillDiscoveryToolsRespectTurnProfileSkills(t *testing.T) {
	t.Setenv("PICOCLAW_BUILTIN_SKILLS", t.TempDir())
	workspace := t.TempDir()
	writeTurnProfileSkill(
		t,
		workspace,
		"shell",
		"---\ndescription: run shell commands\n---\n# shell\n\nUse shell carefully.",
	)
	writeTurnProfileSkill(t, workspace, "paint", "---\ndescription: paint pictures\n---\n# paint\n\nUse paint vividly.")

	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
		Workspace: workspace,
		TurnProfile: config.TurnProfileConfig{
			Enabled: true,
			History: config.TurnProfileBlock{Mode: config.TurnProfileModeOff},
			Skills:  config.TurnProfileBlock{Mode: config.TurnProfileModeCustom, Allow: []string{"shell"}},
		},
	}}}
	provider := &skillToolScriptProvider{script: []providers.ToolCall{
		{Name: "skills_search", Arguments: map[string]any{"query": "paint pictures"}},
		{Name: "skills_search", Arguments: map[string]any{"query": "shell commands"}},
		{Name: "skills_read", Arguments: map[string]any{"name": "shell"}},
		{Name: "skills_read", Arguments: map[string]any{"name": "paint"}},
	}}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)

	if _, err := al.runAgentLoop(context.Background(), al.GetRegistry().GetDefaultAgent(), processOptions{
		SessionKey:      "agent:default:test-skills-tools",
		UserMessage:     "do something",
		DefaultResponse: defaultResponse,
	}); err != nil {
		t.Fatalf("runAgentLoop() error = %v", err)
	}

	got := provider.toolResults()
	if len(got) != 4 {
		t.Fatalf("tool results = %q, want 4", got)
	}
	if got[0] != `{"skills":[],"hasMore":false}` {
		t.Errorf("search for a disallowed skill leaked: %s", got[0])
	}
	if !strings.Contains(got[1], `"name":"shell"`) || strings.Contains(got[1], "paint") {
		t.Errorf("search for the allowed skill = %s", got[1])
	}
	if strings.TrimSpace(got[2]) != "# shell\n\nUse shell carefully." {
		t.Errorf("skills_read(shell) = %q", got[2])
	}
	if !strings.Contains(got[3], "skill not found") || strings.Contains(got[3], "vividly") {
		t.Errorf("skills_read(paint) = %q, want not found", got[3])
	}
}

func TestSystemPromptDoesNotGrowWithInstalledSkills(t *testing.T) {
	t.Setenv("PICOCLAW_BUILTIN_SKILLS", t.TempDir())
	workspace := t.TempDir()
	empty := NewContextBuilder(workspace).BuildSystemPromptWithCache()

	for i := 0; i < 300; i++ {
		writeTurnProfileSkill(t, workspace, fmt.Sprintf("bulk-skill-%03d", i),
			fmt.Sprintf("---\ndescription: bulk skill number %d\n---\n# bulk", i))
	}
	full := NewContextBuilder(workspace).BuildSystemPromptWithCache()

	if full != empty {
		t.Fatalf("prompt changed with 300 installed skills: %d -> %d bytes", len(empty), len(full))
	}
	for _, want := range []string{
		"## Installed Skills", "`skills_search`", "`skills_read`", "Claim a skill was loaded only after a successful read",
	} {
		if !strings.Contains(full, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
