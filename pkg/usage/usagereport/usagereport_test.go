package usagereport

import (
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/usage"
)

func entry(key string, aliases []string, total int) memory.SessionUsageEntry {
	return memory.SessionUsageEntry{
		Key:     key,
		Aliases: aliases,
		Usage:   usage.Summary{Totals: usage.Totals{InputTokens: total, TotalTokens: total}},
	}
}

func TestOverviewSkipsEmptySortsAndSums(t *testing.T) {
	rep := Overview("/sessions", []memory.SessionUsageEntry{
		entry("small", nil, 10),
		{Key: "empty"},
		entry("big", nil, 90),
	})

	if rep.SessionCount != 2 || len(rep.Sessions) != 2 {
		t.Fatalf("sessions = %d/%d, want 2", rep.SessionCount, len(rep.Sessions))
	}
	if rep.Sessions[0].Key != "big" || rep.Sessions[1].Key != "small" {
		t.Errorf("order = %s, %s; want big, small", rep.Sessions[0].Key, rep.Sessions[1].Key)
	}
	if rep.Totals.TotalTokens != 100 {
		t.Errorf("total tokens = %d, want 100", rep.Totals.TotalTokens)
	}
}

func TestOverviewEmptyListsAsSlice(t *testing.T) {
	if rep := Overview("/sessions", nil); rep.Sessions == nil {
		t.Fatal("Sessions is nil, want an empty slice so JSON renders []")
	}
}

func TestSessionLabelPrefersShortestAlias(t *testing.T) {
	key := "sk_v1_abc"
	got := sessionLabel(key, []string{mainSessionAlias, "agent:main:telegram:direct:1", "cli:default"})
	if got != "cli:default" {
		t.Errorf("label = %q, want cli:default", got)
	}
	if got := sessionLabel(key, []string{mainSessionAlias}); got != key {
		t.Errorf("label = %q, want the key when only the shared alias exists", got)
	}
	if got := sessionLabel("legacy", []string{"x"}); got != "legacy" {
		t.Errorf("label = %q, want the legacy key itself", got)
	}
}

func TestOverviewMergesModelsAcrossSessions(t *testing.T) {
	withModels := func(key string, models ...usage.ModelTotals) memory.SessionUsageEntry {
		var summary usage.Summary
		for _, m := range models {
			summary.Merge(usage.Summary{Totals: m.Totals, Models: []usage.ModelTotals{m}})
		}
		return memory.SessionUsageEntry{Key: key, Usage: summary}
	}
	model := func(name string, total int) usage.ModelTotals {
		return usage.ModelTotals{Model: name, Totals: usage.Totals{TotalTokens: total}}
	}

	rep := Overview("/sessions", []memory.SessionUsageEntry{
		withModels("a", model("small", 10), model("big", 50)),
		withModels("b", model("big", 40)),
	})

	if len(rep.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(rep.Models))
	}
	if rep.Models[0].Model != "big" || rep.Models[0].TotalTokens != 90 {
		t.Errorf("first model = %+v, want big with 90 tokens", rep.Models[0])
	}
	if rep.Models[1].Model != "small" || rep.Models[1].TotalTokens != 10 {
		t.Errorf("second model = %+v, want small with 10 tokens", rep.Models[1])
	}
}

func TestAddPeriodWindowsLedgerByCalendarDays(t *testing.T) {
	dir := t.TempDir()
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{
		now.AddDate(0, 0, -10), // before the window, still sets TrackedSince
		now.AddDate(0, 0, -2),  // first day of a 3-day window
		now,
	} {
		msg := providers.Message{
			Role:      "assistant",
			ModelName: "m",
			CreatedAt: &at,
			Usage:     &providers.UsageInfo{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11},
		}
		if err := store.AddFullMessage(t.Context(), "s", msg); err != nil {
			t.Fatal(err)
		}
	}

	rep := Overview(dir, nil)
	if err := rep.AddPeriod(dir, 3, now); err != nil {
		t.Fatalf("AddPeriod: %v", err)
	}
	p := rep.Period
	if p.Totals.TotalTokens != 22 || len(p.Daily) != 2 {
		t.Fatalf("period = %+v, want the 2 days inside the window", p)
	}
	if p.Daily[0].Date != "2026-11-19" || p.Daily[1].Date != "2026-11-21" {
		t.Errorf("days = %s, %s", p.Daily[0].Date, p.Daily[1].Date)
	}
	if p.TrackedSince == nil || p.TrackedSince.Format(usage.DateLayout) != "2026-11-11" {
		t.Errorf("TrackedSince = %v, want the oldest entry", p.TrackedSince)
	}
	if len(p.Models) != 1 || p.Models[0].Model != "m" {
		t.Errorf("models = %+v", p.Models)
	}
	if err := rep.AddPeriod(dir, 0, now); err == nil {
		t.Error("AddPeriod(0) must fail")
	}
}
