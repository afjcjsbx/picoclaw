package usage

import (
	"reflect"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestSummaryAddMessageSingleModel(t *testing.T) {
	var s Summary
	added := s.AddMessage("model-a", providers.UsageInfo{
		PromptTokens:     100,
		CompletionTokens: 20,
		TotalTokens:      120,
		CacheReadTokens:  40,
		CostUSD:          0.001,
		Requests:         2,
	})
	if !added {
		t.Fatal("AddMessage() = false, want true for a message with usage")
	}

	want := Totals{
		Requests:        2,
		Turns:           1,
		InputTokens:     100,
		OutputTokens:    20,
		TotalTokens:     120,
		CacheReadTokens: 40,
		CostUSD:         0.001,
	}
	if s.Totals != want {
		t.Fatalf("Totals = %+v, want %+v", s.Totals, want)
	}
	wantModels := []ModelTotals{{Model: "model-a", Totals: Totals{
		Requests:        2,
		Turns:           1,
		InputTokens:     100,
		OutputTokens:    20,
		TotalTokens:     120,
		CacheReadTokens: 40,
		CostUSD:         0.001,
	}}}
	if !reflect.DeepEqual(s.Models, wantModels) {
		t.Fatalf("Models = %+v, want %+v", s.Models, wantModels)
	}
}

func TestSummaryAddMessageSplitsMultiModelTurns(t *testing.T) {
	var s Summary
	s.AddMessage("fallback-name", providers.UsageInfo{
		ByModel: []providers.ModelUsage{
			{Model: "light", Usage: providers.UsageInfo{PromptTokens: 220, CompletionTokens: 20, TotalTokens: 240, Requests: 2}},
			{Model: "primary", Usage: providers.UsageInfo{PromptTokens: 300, CompletionTokens: 40, TotalTokens: 340, CostUSD: 0.01, Requests: 1}},
		},
	})

	if s.Turns != 1 {
		t.Fatalf("Turns = %d, want 1 per message", s.Turns)
	}
	if s.Requests != 3 || s.InputTokens != 520 || s.OutputTokens != 60 || s.TotalTokens != 580 {
		t.Fatalf("Totals = %+v", s.Totals)
	}
	s.SortModels()
	if len(s.Models) != 2 || s.Models[0].Model != "primary" || s.Models[1].Model != "light" {
		t.Fatalf("Models = %+v, want primary first by total tokens", s.Models)
	}
	for _, m := range s.Models {
		if m.Turns != 1 {
			t.Fatalf("model %q Turns = %d, want 1", m.Model, m.Turns)
		}
	}
	if s.Models[1].Requests != 2 {
		t.Fatalf("light Requests = %d, want 2", s.Models[1].Requests)
	}
}

func TestSummaryAddMessageIgnoresEmptyUsage(t *testing.T) {
	var s Summary
	if s.AddMessage("model", providers.UsageInfo{}) {
		t.Fatal("AddMessage() = true for empty usage")
	}
	if !s.Empty() || len(s.Models) != 0 {
		t.Fatalf("summary = %+v, want empty", s)
	}
}

func TestSummaryMergeCombinesModelRows(t *testing.T) {
	var a, b Summary
	a.AddMessage("shared", providers.UsageInfo{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11})
	b.AddMessage("shared", providers.UsageInfo{PromptTokens: 20, CompletionTokens: 2, TotalTokens: 22})
	b.AddMessage("other", providers.UsageInfo{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6})

	a.Merge(b)
	if a.Turns != 3 || a.InputTokens != 35 || a.TotalTokens != 39 {
		t.Fatalf("merged totals = %+v", a.Totals)
	}
	if len(a.Models) != 2 {
		t.Fatalf("Models = %+v, want two rows", a.Models)
	}
	if a.Models[0].Model != "shared" || a.Models[0].InputTokens != 30 || a.Models[0].Turns != 2 {
		t.Fatalf("shared row = %+v", a.Models[0])
	}
}

func TestFromMessagesIgnoresMessagesWithoutUsage(t *testing.T) {
	summary := FromMessages([]providers.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello", ModelName: "m", Usage: &providers.UsageInfo{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}},
	})
	if summary.Turns != 1 || summary.TotalTokens != 10 {
		t.Fatalf("FromMessages() = %+v", summary.Totals)
	}
}

func TestTotalsFreshInputTokensNeverNegative(t *testing.T) {
	if got := (Totals{InputTokens: 10, CacheReadTokens: 20}).FreshInputTokens(); got != 0 {
		t.Fatalf("FreshInputTokens() = %d, want 0", got)
	}
}

func TestFormatTokens(t *testing.T) {
	cases := map[int]string{
		0:       "0",
		999:     "999",
		1000:    "1,000",
		1234567: "1,234,567",
		-1234:   "-1,234",
	}
	for in, want := range cases {
		if got := FormatTokens(in); got != want {
			t.Fatalf("FormatTokens(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatCost(t *testing.T) {
	cases := map[float64]string{
		0:       "not reported",
		0.00005: "<$0.0001",
		0.014:   "$0.0140",
		1.5:     "$1.50",
	}
	for in, want := range cases {
		if got := FormatCost(in); got != want {
			t.Fatalf("FormatCost(%v) = %q, want %q", in, got, want)
		}
	}
}
