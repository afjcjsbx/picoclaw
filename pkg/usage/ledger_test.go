package usage

import (
	"testing"
	"time"
)

func TestDailyTotalsGroupsByLocalDay(t *testing.T) {
	loc := time.FixedZone("plus2", 2*60*60)
	at := func(day, hour int) time.Time { return time.Date(2026, 11, day, hour, 0, 0, 0, time.UTC) }
	entry := func(ts time.Time, tokens int) LedgerEntry {
		return LedgerEntry{Time: ts, Totals: Totals{TotalTokens: tokens}}
	}

	days := DailyTotals([]LedgerEntry{
		entry(at(21, 10), 5),
		entry(at(20, 23), 7), // 01:00 on the 21st in plus2
		entry(at(20, 10), 3),
	}, loc)

	if len(days) != 2 || days[0].Date != "2026-11-20" || days[1].Date != "2026-11-21" {
		t.Fatalf("days = %+v, want 20th then 21st", days)
	}
	if days[0].TotalTokens != 3 || days[1].TotalTokens != 12 {
		t.Errorf("totals = %d, %d; want 3 and 12", days[0].TotalTokens, days[1].TotalTokens)
	}
}

func TestModelsOfSumsAndSorts(t *testing.T) {
	models := ModelsOf([]LedgerEntry{
		{Model: "small", Totals: Totals{TotalTokens: 5}},
		{Model: "big", Totals: Totals{TotalTokens: 7}},
		{Model: "small", Totals: Totals{TotalTokens: 5}},
	})
	if len(models) != 2 || models[0].Model != "small" || models[0].TotalTokens != 10 {
		t.Fatalf("models = %+v, want small (10) first", models)
	}
}
