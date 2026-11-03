package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// failingVisionProvider always fails like an upstream outage on the image model.
type failingVisionProvider struct {
	calls int
}

func (p *failingVisionProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	return nil, fmt.Errorf(`API request failed: Status: 400 Body: {"error":{"message":"Provider returned error"}}`)
}

func (p *failingVisionProvider) GetDefaultModel() string { return "failing-vision" }

// loadThenAnswerProvider asks for load_image first, then answers from whatever
// the follow-up request contains.
type loadThenAnswerProvider struct {
	path      string
	calls     int
	mediaSeen []bool
	omitNote  bool
}

func (p *loadThenAnswerProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	if p.calls == 1 {
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{
				ID:        "call_load_image_test",
				Type:      "function",
				Name:      "load_image",
				Arguments: map[string]any{"path": p.path},
			}},
		}, nil
	}
	hasMedia := false
	for _, msg := range messages {
		hasMedia = hasMedia || len(msg.Media) > 0
		p.omitNote = p.omitNote || msg.Content == imageUnavailableNote
	}
	p.mediaSeen = append(p.mediaSeen, hasMedia)
	return &providers.LLMResponse{Content: "base answer"}, nil
}

func (p *loadThenAnswerProvider) GetDefaultModel() string { return "load-then-answer" }

func TestAgentLoop_ImageModelFailureFallsBackToBaseModel(t *testing.T) {
	workspace := t.TempDir()
	pngPath := filepath.Join(workspace, "sample.png")
	pngBytes := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02,
		0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE,
	}
	if err := os.WriteFile(pngPath, pngBytes, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				ModelName:         "text-model",
				ImageModel:        "vision-model",
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
		},
		Tools: config.ToolsConfig{
			LoadImage: config.ToolConfig{Enabled: true},
		},
		ModelList: []*config.ModelConfig{
			{ModelName: "text-model", Model: "openai/text-model"},
			{ModelName: "vision-model", Model: "openai/vision-model"},
		},
	}

	base := &loadThenAnswerProvider{path: pngPath}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), base)
	al.SetMediaStore(media.NewFileMediaStore())

	agent := al.registry.GetDefaultAgent()
	if agent == nil || len(agent.ImageCandidates) != 1 {
		t.Fatalf("expected default agent with one image candidate")
	}
	vision := &failingVisionProvider{}
	agent.CandidateProviders[candidateProviderKey(agent.ImageCandidates[0])] = vision

	timeoutCtx, cancel := context.WithTimeout(context.Background(), responseTimeout)
	defer cancel()

	resp, err := al.processMessage(timeoutCtx, testInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:   "telegram",
			ChatID:    "chat1",
			ChatType:  "direct",
			SenderID:  "user1",
			MessageID: "m1",
		},
		Content:    "describe the image you load",
		SessionKey: "agent:main:telegram:direct:user1",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if resp != "base answer" {
		t.Fatalf("response = %q, want %q", resp, "base answer")
	}
	if vision.calls != 1 {
		t.Fatalf("vision calls = %d, want 1", vision.calls)
	}
	if !slices.Equal(base.mediaSeen, []bool{false}) {
		t.Fatalf("base model mediaSeen = %v, want [false]", base.mediaSeen)
	}
	if !base.omitNote {
		t.Fatal("base model should be told the image was omitted")
	}
}

func TestDropInlineMedia(t *testing.T) {
	in := []providers.Message{
		{Role: "user", Content: "hi"},
		toolImageFollowUpPromptMessage([]string{"data:image/png;base64,AAAA"}),
		{Role: "user", Content: "look", Media: []string{"data:image/png;base64,BBBB"}},
	}

	out := dropInlineMedia(in)

	if len(in[1].Media) != 1 {
		t.Fatal("input messages must not be mutated")
	}
	if out[1].Content != imageUnavailableNote || len(out[1].Media) != 0 {
		t.Fatalf("follow-up = %+v, want omission note without media", out[1])
	}
	if out[2].Content != "look" || len(out[2].Media) != 0 {
		t.Fatalf("user message = %+v, want content kept and media dropped", out[2])
	}
}
