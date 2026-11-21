package usagereport

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/memory"
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
