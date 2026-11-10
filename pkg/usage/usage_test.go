package usage

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestFromMessagesAggregatesTotalsAndModels(t *testing.T) {
	messages := []providers.Message{
		{
			Role:      "user",
			Content:   "hello",
			ModelName: "model-a",
		},
		{
			Role:      "assistant",
			ModelName: "model-a",
			Usage: &providers.UsageInfo{
				PromptTokens:     120,
				CompletionTokens: 30,
				TotalTokens:      150,
				CacheReadTokens:  10,
				CostUSD:          0.001,
				Requests:         1,
			},
		},
		{
			Role:      "assistant",
			ModelName: "model-b",
			Usage: &providers.UsageInfo{
				PromptTokens:        200,
				CompletionTokens:    50,
				CacheCreationTokens: 20,
				CostUSD:             0.002,
				Requests:            2,
			},
		},
		{
			// Empty usage must be ignored.
			Role:  "assistant",
			Usage: &providers.UsageInfo{},
		},
	}

	summary := FromMessages(messages)

	if summary.Requests != 3 || summary.Turns != 2 {
		t.Fatalf("requests/turns = %d/%d, want 3/2", summary.Requests, summary.Turns)
	}
	if summary.InputTokens != 320 || summary.OutputTokens != 80 {
		t.Fatalf("input/output = %d/%d, want 320/80", summary.InputTokens, summary.OutputTokens)
	}
	if summary.TotalTokens != 150+250 {
		t.Fatalf("total = %d, want 400", summary.TotalTokens)
	}
	if summary.CacheReadTokens != 10 || summary.CacheCreationTokens != 20 {
		t.Fatalf("cache read/write = %d/%d, want 10/20",
			summary.CacheReadTokens, summary.CacheCreationTokens)
	}
	if summary.CostUSD < 0.002999 || summary.CostUSD > 0.003001 {
		t.Fatalf("cost = %v, want 0.003", summary.CostUSD)
	}

	if len(summary.Models) != 2 {
		t.Fatalf("models = %+v, want 2 entries", summary.Models)
	}
	// model-b has more tokens, so it sorts first.
	if summary.Models[0].Model != "model-b" || summary.Models[1].Model != "model-a" {
		t.Fatalf("model order = %+v, want model-b then model-a", summary.Models)
	}
	if summary.Models[0].Requests != 2 || summary.Models[0].TotalTokens != 250 {
		t.Fatalf("model-b totals = %+v", summary.Models[0].Totals)
	}
	if summary.Models[1].InputTokens != 120 || summary.Models[1].OutputTokens != 30 {
		t.Fatalf("model-a totals = %+v", summary.Models[1].Totals)
	}
}

func TestFromMessagesUsesUnknownModelAndFallbackTotal(t *testing.T) {
	summary := FromMessages([]providers.Message{{
		Role: "assistant",
		Usage: &providers.UsageInfo{
			PromptTokens:     10,
			CompletionTokens: 5,
		},
	}})

	if summary.TotalTokens != 15 {
		t.Fatalf("total = %d, want 15", summary.TotalTokens)
	}
	if summary.Requests != 1 || summary.Turns != 1 {
		t.Fatalf("requests/turns = %d/%d, want 1/1", summary.Requests, summary.Turns)
	}
	if len(summary.Models) != 1 || summary.Models[0].Model != UnknownModel {
		t.Fatalf("models = %+v, want unknown", summary.Models)
	}
}

func TestTotalsAddMergeAndSummaryAdd(t *testing.T) {
	var totals Totals
	if !totals.Add(providers.UsageInfo{
		PromptTokens:     100,
		CompletionTokens: 20,
		TotalTokens:      120,
		CostUSD:          0.01,
		Requests:         1,
	}) {
		t.Fatal("Add() rejected valid usage")
	}
	if totals.Add(providers.UsageInfo{}) {
		t.Fatal("Add() accepted empty usage")
	}

	totals.Merge(Totals{
		Requests:     2,
		Turns:        1,
		InputTokens:  50,
		OutputTokens: 10,
		TotalTokens:  60,
		CostUSD:      0.02,
	})
	if totals.Requests != 3 || totals.InputTokens != 150 || totals.TotalTokens != 180 {
		t.Fatalf("merged totals = %+v", totals)
	}
	if totals.CostUSD < 0.029999 || totals.CostUSD > 0.030001 {
		t.Fatalf("merged cost = %v, want 0.03", totals.CostUSD)
	}

	var summary Summary
	summary.Add("model-a", providers.UsageInfo{PromptTokens: 10, CompletionTokens: 5})
	summary.Add(" model-a ", providers.UsageInfo{PromptTokens: 20, CompletionTokens: 5})
	summary.Add("", providers.UsageInfo{PromptTokens: 30, CompletionTokens: 5})
	if len(summary.Models) != 2 {
		t.Fatalf("models = %+v, want model-a and unknown", summary.Models)
	}
	if summary.Models[0].Requests != 2 || summary.Models[0].InputTokens != 30 {
		t.Fatalf("model-a totals = %+v", summary.Models[0].Totals)
	}
	if summary.Models[1].Model != UnknownModel {
		t.Fatalf("empty model = %q, want %q", summary.Models[1].Model, UnknownModel)
	}
}

func TestEmptyAndInputBreakdown(t *testing.T) {
	var zero Summary
	if !zero.Empty() {
		t.Fatal("zero summary should be empty")
	}
	withRequests := Summary{Totals: Totals{Requests: 1}}
	if withRequests.Empty() {
		t.Fatal("summary with requests should not be empty")
	}

	plain := Totals{InputTokens: 1234}
	if got := plain.InputBreakdown(); got != "1,234 tokens" {
		t.Fatalf("InputBreakdown without cache = %q", got)
	}

	cached := Totals{
		InputTokens:         1000,
		CacheReadTokens:     700,
		CacheCreationTokens: 100,
	}
	if got := cached.FreshInputTokens(); got != 200 {
		t.Fatalf("FreshInputTokens = %d, want 200", got)
	}
	want := "1,000 tokens (fresh 200 · cache read 700 · cache write 100)"
	if got := cached.InputBreakdown(); got != want {
		t.Fatalf("InputBreakdown = %q, want %q", got, want)
	}

	overReported := Totals{InputTokens: 10, CacheReadTokens: 15}
	if got := overReported.FreshInputTokens(); got != 0 {
		t.Fatalf("FreshInputTokens with over-reported cache = %d, want 0", got)
	}
}

func TestFormatTokensAndCost(t *testing.T) {
	tokenCases := map[int]string{
		0:       "0",
		999:     "999",
		1000:    "1,000",
		1234567: "1,234,567",
	}
	for input, want := range tokenCases {
		if got := FormatTokens(input); got != want {
			t.Errorf("FormatTokens(%d) = %q, want %q", input, got, want)
		}
	}

	costCases := map[float64]string{
		0:       "$0",
		0.00001: "<$0.0001",
		0.0123:  "$0.0123",
		1.2345:  "$1.23",
	}
	for input, want := range costCases {
		if got := FormatCostUSD(input); got != want {
			t.Errorf("FormatCostUSD(%v) = %q, want %q", input, got, want)
		}
	}
}
