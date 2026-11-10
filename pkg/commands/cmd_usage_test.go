package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/usage"
)

func executeUsage(t *testing.T, rt *Runtime, text string) string {
	t.Helper()
	var reply string
	result := NewExecutor(NewRegistry(BuiltinDefinitions()), rt).Execute(
		context.Background(),
		Request{
			Text: text,
			Reply: func(out string) error {
				reply = out
				return nil
			},
		},
	)
	if result.Outcome != OutcomeHandled {
		t.Fatalf("outcome = %v, want handled", result.Outcome)
	}
	if result.Err != nil {
		t.Fatalf("execute %s: %v", text, result.Err)
	}
	return reply
}

func TestUsageCommandFormatsSessionUsage(t *testing.T) {
	rt := &Runtime{
		GetUsageStats: func() (*usage.Summary, error) {
			return &usage.Summary{
				Totals: usage.Totals{
					Requests:            3,
					Turns:               2,
					InputTokens:         1500,
					OutputTokens:        250,
					TotalTokens:         1750,
					CacheReadTokens:     1000,
					CacheCreationTokens: 100,
					CostUSD:             0.0123,
				},
				Models: []usage.ModelTotals{
					{Model: "model-a", Totals: usage.Totals{
						Requests: 3, Turns: 2, InputTokens: 1500, OutputTokens: 250, TotalTokens: 1750, CostUSD: 0.0123,
					}},
					{Model: "model-b", Totals: usage.Totals{
						Requests: 1, Turns: 1, InputTokens: 100, OutputTokens: 10, TotalTokens: 110,
					}},
				},
			}, nil
		},
	}

	reply := executeUsage(t, rt, "/usage")
	for _, want := range []string{
		"Usage for this session",
		"LLM calls: 3 · turns: 2",
		"Input: 1,500 tokens (fresh 400 · cache read 1,000 · cache write 100)",
		"Output: 250 tokens",
		"Total: 1,750 tokens",
		"Cost: $0.0123",
		"By model:",
		"- `model-a` — 3 calls · 1,500 in / 250 out · $0.0123",
		"- `model-b` — 1 call · 100 in / 10 out · not reported",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("/usage reply missing %q, got:\n%s", want, reply)
		}
	}
}

func TestUsageCommandSingleModelShowsModelLine(t *testing.T) {
	rt := &Runtime{
		GetUsageStats: func() (*usage.Summary, error) {
			return &usage.Summary{
				Totals: usage.Totals{Requests: 1, Turns: 1, InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
				Models: []usage.ModelTotals{{
					Model:  "only-model",
					Totals: usage.Totals{Requests: 1, Turns: 1, InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
				}},
			}, nil
		},
	}

	reply := executeUsage(t, rt, "/usage")
	if strings.Contains(reply, "By model:") {
		t.Fatalf("/usage should omit the per-model section with one model:\n%s", reply)
	}
	if !strings.Contains(reply, "Model: `only-model`") || !strings.Contains(reply, "Cost: not reported") {
		t.Fatalf("/usage reply = %q", reply)
	}
}

func TestUsageCommandCostAlias(t *testing.T) {
	rt := &Runtime{
		GetUsageStats: func() (*usage.Summary, error) {
			return &usage.Summary{Totals: usage.Totals{
				Requests: 1, Turns: 1, InputTokens: 10, OutputTokens: 5, TotalTokens: 15,
			}}, nil
		},
	}
	if reply := executeUsage(t, rt, "/cost"); !strings.Contains(reply, "Usage for this session") {
		t.Fatalf("/cost reply = %q", reply)
	}
}

func TestUsageCommandNoUsage(t *testing.T) {
	rt := &Runtime{GetUsageStats: func() (*usage.Summary, error) { return nil, nil }}
	if reply := executeUsage(t, rt, "/usage"); reply != "No token usage recorded for this session yet." {
		t.Fatalf("reply = %q", reply)
	}
}

func TestUsageCommandUnavailableWithoutRuntime(t *testing.T) {
	if reply := executeUsage(t, &Runtime{}, "/usage"); reply != unavailableMsg {
		t.Fatalf("reply = %q, want %q", reply, unavailableMsg)
	}
}

func TestUsageCommandReportsReadErrors(t *testing.T) {
	rt := &Runtime{
		GetUsageStats: func() (*usage.Summary, error) {
			return nil, errors.New("disk unavailable")
		},
	}
	if reply := executeUsage(t, rt, "/usage"); !strings.Contains(reply, "disk unavailable") {
		t.Fatalf("reply = %q, want the read error", reply)
	}
}
