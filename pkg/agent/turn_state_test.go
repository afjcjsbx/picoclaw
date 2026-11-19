package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/usage"
)

func TestMatchingTurnMessageTail_IgnoresInternalRuntimeFields(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "question"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{
					ID:   "call_1",
					Type: "function",
					Function: &providers.FunctionCall{
						Name:      "read_file",
						Arguments: `{"path":"/tmp/test"}`,
					},
				},
			},
		},
	}

	persisted := []providers.Message{
		userPromptMessage("question", nil),
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{
					ID:               "call_1",
					Type:             "function",
					Name:             "read_file",
					Arguments:        map[string]any{"path": "/tmp/test"},
					ThoughtSignature: "internal-signature",
					Function: &providers.FunctionCall{
						Name:             "read_file",
						Arguments:        `{"path":"/tmp/test"}`,
						ThoughtSignature: "internal-signature",
					},
				},
			},
		},
	}

	if got := matchingTurnMessageTail(history, persisted); got != 2 {
		t.Fatalf("matchingTurnMessageTail() = %d, want 2", got)
	}
}

func TestSplitHistoryForActiveTurn_ProtectsPersistedTail(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "old question"},
		{Role: "assistant", Content: "old answer"},
		{Role: "user", Content: "current question"},
		{Role: "tool", Content: "tool output", ToolCallID: "call_1"},
	}

	persisted := []providers.Message{
		userPromptMessage("current question", nil),
		{Role: "tool", Content: "tool output", ToolCallID: "call_1"},
	}

	stable, protected := splitHistoryForActiveTurn(history, persisted)
	if len(stable) != 2 {
		t.Fatalf("stable history len = %d, want 2", len(stable))
	}
	if len(protected) != 2 {
		t.Fatalf("protected tail len = %d, want 2", len(protected))
	}
	if protected[0].Content != "current question" {
		t.Fatalf("protected[0].Content = %q, want current question", protected[0].Content)
	}
}

func TestTurnStateAddUsageAggregatesCalls(t *testing.T) {
	ts := &turnState{}
	ts.AddUsage("model-a", nil)
	ts.AddUsage("model-a", &providers.UsageInfo{})
	if got := ts.GetTurnUsage(); got != nil {
		t.Fatalf("GetTurnUsage() = %+v, want nil for empty usage", got)
	}

	ts.AddUsage("model-a", &providers.UsageInfo{
		PromptTokens:        120,
		CompletionTokens:    30,
		TotalTokens:         150,
		CacheReadTokens:     10,
		CacheCreationTokens: 5,
		CostUSD:             0.001,
	})
	ts.AddUsage("model-a", &providers.UsageInfo{
		PromptTokens:     200,
		CompletionTokens: 50,
		TotalTokens:      250,
		CacheReadTokens:  20,
		CostUSD:          0.002,
	})

	want := providers.UsageInfo{
		PromptTokens:        320,
		CompletionTokens:    80,
		TotalTokens:         400,
		CacheReadTokens:     30,
		CacheCreationTokens: 5,
		CostUSD:             0.003,
		Requests:            2,
		ContextTokens:       200,
	}
	if got := ts.GetTurnUsage(); got == nil || !reflect.DeepEqual(*got, want) {
		t.Fatalf("GetTurnUsage() = %+v, want %+v", got, want)
	}
}

func TestTurnStateAddUsageSplitsMultiModelTurns(t *testing.T) {
	ts := &turnState{}
	ts.AddUsage("light", &providers.UsageInfo{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110})
	ts.AddUsage("light", &providers.UsageInfo{PromptTokens: 120, CompletionTokens: 10, TotalTokens: 130})
	ts.AddUsage(
		"primary",
		&providers.UsageInfo{PromptTokens: 300, CompletionTokens: 40, TotalTokens: 340, CostUSD: 0.01},
	)

	got := ts.GetTurnUsage()
	if got == nil {
		t.Fatal("GetTurnUsage() = nil, want aggregate")
	}
	if got.Requests != 3 || got.PromptTokens != 520 || got.ContextTokens != 300 {
		t.Fatalf("aggregate = %+v", got)
	}
	want := []providers.ModelUsage{
		{
			Model: "light",
			Usage: providers.UsageInfo{PromptTokens: 220, CompletionTokens: 20, TotalTokens: 240, Requests: 2},
		},
		{
			Model: "primary",
			Usage: providers.UsageInfo{
				PromptTokens:     300,
				CompletionTokens: 40,
				TotalTokens:      340,
				CostUSD:          0.01,
				Requests:         1,
			},
		},
	}
	if !reflect.DeepEqual(got.ByModel, want) {
		t.Fatalf("ByModel = %+v, want %+v", got.ByModel, want)
	}

	got.ByModel[0].Usage.Requests = 99
	if again := ts.GetTurnUsage(); again.ByModel[0].Usage.Requests != 2 {
		t.Fatal("GetTurnUsage() must return a copy of the per-model split")
	}
}

func TestTurnStateAddUsageLabelsMissingModelUnknown(t *testing.T) {
	ts := &turnState{}
	ts.AddUsage("", &providers.UsageInfo{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6})
	ts.AddUsage("  ", &providers.UsageInfo{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6})
	ts.AddUsage("primary", &providers.UsageInfo{PromptTokens: 7, CompletionTokens: 1, TotalTokens: 8})

	got := ts.GetTurnUsage()
	if got == nil || len(got.ByModel) != 2 {
		t.Fatalf("GetTurnUsage().ByModel = %+v, want unknown and primary", got)
	}
	if split := got.ByModel[0]; split.Model != usage.UnknownModel || split.Usage.Requests != 2 {
		t.Fatalf("ByModel[0] = %+v, want both unnamed calls under %q", split, usage.UnknownModel)
	}
}

func TestTurnStateAddUsageSingleModelOmitsSplit(t *testing.T) {
	ts := &turnState{}
	ts.AddUsage("only", &providers.UsageInfo{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6})
	ts.AddUsage("only", &providers.UsageInfo{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6})
	if got := ts.GetTurnUsage(); got == nil || got.ByModel != nil {
		t.Fatalf("GetTurnUsage().ByModel = %+v, want nil for a single-model turn", got)
	}
}

func TestTrimHistoryToFitContextWindow_WithProtectedTurnTailKeepsActiveTurn(t *testing.T) {
	current := strings.Repeat("current turn ", 80)
	history := []providers.Message{
		{Role: "user", Content: strings.Repeat("old turn ", 60)},
		{Role: "assistant", Content: strings.Repeat("old reply ", 60)},
		{Role: "user", Content: current},
	}

	stable, protected := splitHistoryForActiveTurn(history, []providers.Message{
		userPromptMessage(current, nil),
	})
	trimmedStable, messages, fit := trimHistoryToFitContextWindow(
		stable,
		func(trimmedHistory []providers.Message) []providers.Message {
			return append(append([]providers.Message(nil), trimmedHistory...), protected...)
		},
		120,
		nil,
		0,
	)

	if fit {
		t.Fatal("expected protected active turn alone to remain over budget")
	}
	if len(trimmedStable) != 0 {
		t.Fatalf("trimmed stable history len = %d, want 0", len(trimmedStable))
	}
	if len(messages) != 1 {
		t.Fatalf("messages len = %d, want 1 protected active-turn message", len(messages))
	}
	if messages[0].Content != current {
		t.Fatalf("messages[0].Content = %q, want protected current turn", messages[0].Content)
	}
}
