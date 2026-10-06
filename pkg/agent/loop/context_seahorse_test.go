//go:build !no_seahorse && !mipsle && !netbsd && !(freebsd && arm)

package loop

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/agent/agentctx"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/seahorse"
)

// seahorseTestProvider implements providers.LLMProvider for seahorse tests.
type seahorseTestProvider struct {
	chatFn func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error)
}

func (m *seahorseTestProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	options map[string]any,
) (*providers.LLMResponse, error) {
	if m.chatFn != nil {
		return m.chatFn(ctx, messages, tools, model, options)
	}
	return &providers.LLMResponse{Content: "mock response"}, nil
}

func (m *seahorseTestProvider) GetDefaultModel() string {
	return "mock-model"
}

// TestSeahorseRealLoopNoDuplicateMessages tests the real-world scenario:
// 1. Start AgentLoop with seahorse context manager
// 2. Run a turn (user message -> LLM response)
// 3. Check DB for duplicate messages
// This test verifies that bootstrapping at startup (not during first Ingest) prevents duplicates.
func TestSeahorseRealLoopNoDuplicateMessages(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ContextManager:    "seahorse",
			},
		},
	}

	msgBus := bus.NewMessageBus()
	mockProvider := &simpleMockProvider{response: "I received your message."}
	al := NewAgentLoop(cfg, msgBus, mockProvider)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	ctx := context.Background()
	sessionKey := "test-real-loop-dup"

	// Run a turn: user message -> LLM response
	_, err := al.runAgentLoop(ctx, defaultAgent, processOptions{
		SessionKey:      sessionKey,
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "hello",
		DefaultResponse: defaultResponse,
		EnableSummary:   false,
		SendResponse:    false,
	})
	if err != nil {
		t.Fatalf("runAgentLoop failed: %v", err)
	}

	// Get the seahorse engine from context manager
	seahorseCM, ok := al.contextManager.(*agentctx.SeahorseContextManager)
	if !ok {
		t.Fatal("expected agentctx.SeahorseContextManager")
	}

	// Check DB for messages via RetrievalEngine.Store()
	store := seahorseCM.Engine().GetRetrieval().Store()
	conv, err := store.GetOrCreateConversation(ctx, sessionKey)
	if err != nil {
		t.Fatalf("GetOrCreateConversation: %v", err)
	}

	stored, err := store.GetMessages(ctx, conv.ConversationID, 20, 0)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}

	t.Logf("DB has %d messages:", len(stored))
	for i, msg := range stored {
		content := msg.Content
		if len(content) > 40 {
			content = content[:40] + "..."
		}
		t.Logf("  msg[%d]: role=%s content=%q", i, msg.Role, content)
	}

	// Count duplicates by (role, content)
	seen := make(map[string]int)
	for _, msg := range stored {
		key := msg.Role + ":" + msg.Content
		seen[key]++
	}
	for key, count := range seen {
		if count > 1 {
			t.Errorf("DUPLICATE BUG: %q appears %d times in DB", key, count)
		}
	}

	// Expected: 2 messages (user "hello" + assistant response)
	if len(stored) != 2 {
		t.Errorf("expected 2 messages in DB (user + assistant), got %d", len(stored))
	}
}

// TestSeahorseSteeringMessageIngested verifies that steering messages are ingested
// into seahorse SQLite, not just session JSONL.
func TestSeahorseSteeringMessageIngested(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ContextManager:    "seahorse",
			},
		},
	}

	msgBus := bus.NewMessageBus()
	mockProvider := &simpleMockProvider{response: "I received your message."}
	al := NewAgentLoop(cfg, msgBus, mockProvider)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	ctx := context.Background()
	sessionKey := "test-steering-ingest"

	// First turn: establish conversation
	_, err := al.runAgentLoop(ctx, defaultAgent, processOptions{
		SessionKey:      sessionKey,
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "hello",
		DefaultResponse: defaultResponse,
		EnableSummary:   false,
		SendResponse:    false,
	})
	if err != nil {
		t.Fatalf("first runAgentLoop failed: %v", err)
	}

	// Inject a steering message
	steerErr := al.InjectSteering(providers.Message{
		Role:    "user",
		Content: "steering message content",
	})
	if steerErr != nil {
		t.Fatalf("InjectSteering failed: %v", steerErr)
	}

	// Second turn: should process steering message
	_, err = al.runAgentLoop(ctx, defaultAgent, processOptions{
		SessionKey:      sessionKey,
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "continue",
		DefaultResponse: defaultResponse,
		EnableSummary:   false,
		SendResponse:    false,
	})
	if err != nil {
		t.Fatalf("second runAgentLoop failed: %v", err)
	}

	// Get the seahorse engine from context manager
	seahorseCM, ok := al.contextManager.(*agentctx.SeahorseContextManager)
	if !ok {
		t.Fatal("expected agentctx.SeahorseContextManager")
	}

	// Check DB for steering message
	store := seahorseCM.Engine().GetRetrieval().Store()
	conv, err := store.GetOrCreateConversation(ctx, sessionKey)
	if err != nil {
		t.Fatalf("GetOrCreateConversation: %v", err)
	}

	stored, err := store.GetMessages(ctx, conv.ConversationID, 20, 0)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}

	t.Logf("DB has %d messages:", len(stored))
	for i, msg := range stored {
		content := msg.Content
		if len(content) > 40 {
			content = content[:40] + "..."
		}
		t.Logf("  msg[%d]: role=%s content=%q", i, msg.Role, content)
	}

	// Find steering message in stored messages
	foundSteering := false
	for _, msg := range stored {
		if msg.Content == "steering message content" {
			foundSteering = true
			break
		}
	}

	if !foundSteering {
		t.Error("STEERING MESSAGE NOT IN SEAHORSE DB: steering message should be ingested into SQLite")
	}
}

// TestSeahorseSummarizeSkipsCondensedWhenBelowThreshold verifies that when
// Summarize is triggered but tokens are below ContextWindow threshold,
// condensed compaction should NOT run.
func TestSeahorseSummarizeSkipsCondensedWhenBelowThreshold(t *testing.T) {
	contextWindow := 1000
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ContextManager:    "seahorse",
				ContextWindow:     contextWindow,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &seahorseTestProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	ctx := context.Background()
	sessionKey := "test-summarize-skip-condensed"

	seahorseCM, ok := al.contextManager.(*agentctx.SeahorseContextManager)
	if !ok {
		t.Fatal("expected agentctx.SeahorseContextManager")
	}
	store := seahorseCM.Engine().GetRetrieval().Store()

	conv, err := store.GetOrCreateConversation(ctx, sessionKey)
	if err != nil {
		t.Fatalf("GetOrCreateConversation: %v", err)
	}

	// Insert leaf summaries directly (bypass leaf compaction requirement)
	for i := 0; i < seahorse.CondensedMinFanout; i++ {
		now := time.Now().UTC()
		summary, sumErr := store.CreateSummary(ctx, seahorse.CreateSummaryInput{
			ConversationID: conv.ConversationID,
			Kind:           seahorse.SummaryKindLeaf,
			Depth:          0,
			Content:        fmt.Sprintf("leaf summary %d", i),
			TokenCount:     50,
			EarliestAt:     &now,
			LatestAt:       &now,
		})
		if sumErr != nil {
			t.Fatalf("CreateSummary %d: %v", i, sumErr)
		}
		if appendErr := store.AppendContextSummary(ctx, conv.ConversationID, summary.SummaryID); appendErr != nil {
			t.Fatalf("AppendContextSummary %d: %v", i, appendErr)
		}
	}

	// Add fresh messages (required for condensation candidates)
	for i := 0; i < seahorse.FreshTailCount+1; i++ {
		m, msgErr := store.AddMessage(ctx, conv.ConversationID, "user", "fresh", 5)
		if msgErr != nil {
			t.Fatalf("AddMessage %d: %v", i, msgErr)
		}
		if appendErr := store.AppendContextMessage(ctx, conv.ConversationID, m.ID); appendErr != nil {
			t.Fatalf("AppendContextMessage %d: %v", i, appendErr)
		}
	}

	tokensBefore, err := store.GetContextTokenCount(ctx, conv.ConversationID)
	if err != nil {
		t.Fatalf("GetContextTokenCount: %v", err)
	}
	threshold := int(float64(contextWindow) * seahorse.ContextThreshold)
	t.Logf("Tokens before: %d, threshold: %d", tokensBefore, threshold)

	// Trigger Summarize
	_, err = al.runAgentLoop(ctx, defaultAgent, processOptions{
		SessionKey:      sessionKey,
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "trigger",
		DefaultResponse: defaultResponse,
		EnableSummary:   true,
		SendResponse:    false,
	})
	if err != nil {
		t.Fatalf("runAgentLoop: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	summaries, err := store.GetSummariesByConversation(ctx, conv.ConversationID)
	if err != nil {
		t.Fatalf("GetSummariesByConversation: %v", err)
	}

	condensedCount := 0
	for _, sum := range summaries {
		if sum.Kind == seahorse.SummaryKindCondensed {
			condensedCount++
		}
	}

	t.Logf("Condensed summaries: %d", condensedCount)

	if tokensBefore < threshold && condensedCount > 0 {
		t.Errorf("BUG: condensed created when tokens (%d) < threshold (%d)", tokensBefore, threshold)
	}
}
