package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// adaptiveMockProvider routes responses based on the model parameter.
type adaptiveMockProvider struct {
	calls      atomic.Int32
	localResp  string
	localErr   error
	cloudResp  string
	cloudErr   error
	localModel string
	cloudModel string
}

func (m *adaptiveMockProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.calls.Add(1)

	if model == m.localModel {
		if m.localErr != nil {
			return nil, m.localErr
		}
		return &providers.LLMResponse{
			Content:      m.localResp,
			FinishReason: "stop",
		}, nil
	}
	if model == m.cloudModel {
		if m.cloudErr != nil {
			return nil, m.cloudErr
		}
		return &providers.LLMResponse{
			Content:      m.cloudResp,
			FinishReason: "stop",
		}, nil
	}
	return nil, fmt.Errorf("unexpected model: %s", model)
}

func (m *adaptiveMockProvider) GetDefaultModel() string {
	return m.localModel
}

func newAdaptiveTestSetup(t *testing.T, provider *adaptiveMockProvider) (*AgentLoop, *config.Config, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "agent-adaptive-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             provider.localModel,
				MaxTokens:         4096,
				MaxToolIterations: 10,
				AdaptiveRouting: &config.AdaptiveRoutingConfig{
					Enabled:                  true,
					LocalFirstModel:          provider.localModel,
					CloudEscalationModel:     provider.cloudModel,
					MaxEscalations:           1,
					BypassOnExplicitOverride: true,
					Validation: config.AdaptiveValidationConfig{
						Mode:     "heuristic",
						MinScore: 0.75,
					},
				},
			},
		},
		ModelList: []config.ModelConfig{
			{
				ModelName: provider.localModel,
				Model:     "openai/" + provider.localModel,
			},
			{
				ModelName: provider.cloudModel,
				Model:     "openai/" + provider.cloudModel,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, provider)
	return al, cfg, func() { os.RemoveAll(tmpDir) }
}

func TestAdaptiveRouting_LocalSuccess_NoEscalation(t *testing.T) {
	provider := &adaptiveMockProvider{
		localModel: "local-model",
		cloudModel: "cloud-model",
		localResp:  "Hello from local!",
		cloudResp:  "Hello from cloud!",
	}

	al, _, cleanup := newAdaptiveTestSetup(t, provider)
	defer cleanup()

	helper := testHelper{al: al}
	msg := bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "hello",
		Peer:     bus.Peer{Kind: "direct", ID: "user1"},
	}

	response := helper.executeAndGetResponse(t, context.Background(), msg)
	if response != "Hello from local!" {
		t.Errorf("response = %q, want %q", response, "Hello from local!")
	}

	// Only local model should have been called (1 call)
	if c := provider.calls.Load(); c != 1 {
		t.Errorf("calls = %d, want 1 (local only)", c)
	}
}

func TestAdaptiveRouting_LocalFails_EscalatesToCloud(t *testing.T) {
	provider := &adaptiveMockProvider{
		localModel: "local-model",
		cloudModel: "cloud-model",
		localErr:   errors.New("connection refused"),
		cloudResp:  "Hello from cloud!",
	}

	al, _, cleanup := newAdaptiveTestSetup(t, provider)
	defer cleanup()

	helper := testHelper{al: al}
	msg := bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "hello",
		Peer:     bus.Peer{Kind: "direct", ID: "user1"},
	}

	response := helper.executeAndGetResponse(t, context.Background(), msg)
	if response != "Hello from cloud!" {
		t.Errorf("response = %q, want %q", response, "Hello from cloud!")
	}

	// 2 calls: local fail + cloud success
	if c := provider.calls.Load(); c != 2 {
		t.Errorf("calls = %d, want 2 (local fail + cloud success)", c)
	}
}

func TestAdaptiveRouting_LocalEmpty_EscalatesToCloud(t *testing.T) {
	provider := &adaptiveMockProvider{
		localModel: "local-model",
		cloudModel: "cloud-model",
		localResp:  "", // empty response triggers escalation
		cloudResp:  "Cloud response!",
	}

	al, _, cleanup := newAdaptiveTestSetup(t, provider)
	defer cleanup()

	helper := testHelper{al: al}
	msg := bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "hello",
		Peer:     bus.Peer{Kind: "direct", ID: "user1"},
	}

	response := helper.executeAndGetResponse(t, context.Background(), msg)
	// Empty local response gets the default response from the LLM iteration,
	// but the adaptive validator catches it and escalates.
	// Actually, the empty content goes through runLLMIteration which returns ""
	// as finalContent. The adaptive validator sees this as empty output.
	// Note: the defaultResponse logic is in runAgentLoop, after runLLMIteration returns.
	// So runLLMIteration returns "" → adaptive validator sees empty → escalates.
	if response != "Cloud response!" {
		t.Errorf("response = %q, want %q", response, "Cloud response!")
	}
}

func TestAdaptiveRouting_BypassOnExplicitOverride(t *testing.T) {
	provider := &adaptiveMockProvider{
		localModel: "local-model",
		cloudModel: "cloud-model",
		localResp:  "Hello from local!",
		cloudResp:  "Hello from cloud!",
	}

	al, _, cleanup := newAdaptiveTestSetup(t, provider)
	defer cleanup()

	// First, switch model to trigger bypassOnExplicitOverride
	helper := testHelper{al: al}
	switchResp := helper.executeAndGetResponse(t, context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/switch model to custom-model",
		Peer:     bus.Peer{Kind: "direct", ID: "user1"},
	})
	if switchResp == "" {
		t.Fatal("switch command returned empty response")
	}

	// After switch, the default agent's ModelSwitched flag should be true
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("no default agent")
	}
	if !defaultAgent.ModelSwitched {
		t.Error("expected ModelSwitched to be true after /switch")
	}

	// resolveAdaptiveRunner should return nil now (bypassed)
	if runner := al.resolveAdaptiveRunner(defaultAgent); runner != nil {
		t.Error("expected adaptive runner to be bypassed after model switch")
	}
}

func TestAdaptiveRouting_Disabled(t *testing.T) {
	provider := &adaptiveMockProvider{
		localModel: "local-model",
		cloudModel: "cloud-model",
		localResp:  "Hello from local!",
	}

	tmpDir, err := os.MkdirTemp("", "agent-adaptive-disabled-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "local-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				// No AdaptiveRouting config → disabled
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, provider)

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("no default agent")
	}
	if defaultAgent.AdaptiveRunner != nil {
		t.Error("expected AdaptiveRunner to be nil when adaptive routing is not configured")
	}
}
