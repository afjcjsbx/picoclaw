package commands

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFormatSubagentsReply_Empty(t *testing.T) {
	got := FormatSubagentsReply(nil, time.Now())
	want := "Nothing is running in this chat right now."
	if got != want {
		t.Fatalf("empty reply=%q, want=%q", got, want)
	}
}

func TestFormatSubagentsReply_Tree(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	turns := []TurnInfo{
		{
			TurnID:      "root",
			AgentID:     "main",
			UserMessage: "Write a report\n  on   sales",
			Phase:       "running",
			StartedAt:   now.Add(-72 * time.Second),
		},
		{
			TurnID:       "sub-1",
			ParentTurnID: "root",
			AgentID:      "researcher",
			UserMessage:  "Find sources",
			Phase:        "tools",
			StartedAt:    now.Add(-12 * time.Second),
		},
		{
			TurnID:       "sub-2",
			ParentTurnID: "root",
			AgentID:      "writer",
			Phase:        "setup",
			StartedAt:    now.Add(-2 * time.Second),
		},
		{
			TurnID:       "sub-1a",
			ParentTurnID: "sub-1",
			AgentID:      "fetcher",
			UserMessage:  "Fetch page",
			Phase:        "running",
			StartedAt:    now.Add(-5 * time.Second),
		},
	}

	got := FormatSubagentsReply(turns, now)
	want := strings.Join([]string{
		"**Running in this chat:** 1 main task, 3 subagents",
		"Main task = the request being handled. Subagents = helper tasks it started.",
		"",
		"```text",
		"Main task · agent: main · thinking · 1m12s",
		`  "Write a report on sales"`,
		"  ├─ Subagent 1 · agent: researcher · using tools · 12s",
		`  │  "Find sources"`,
		"  │  └─ Subagent 2 · agent: fetcher · thinking · 5s",
		`  │     "Fetch page"`,
		"  └─ Subagent 3 · agent: writer · starting · 2s",
		"```",
	}, "\n")
	if got != want {
		t.Fatalf("tree reply mismatch\ngot:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestFormatSubagentsReply_MainTaskOnly(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	got := FormatSubagentsReply([]TurnInfo{
		{TurnID: "root", AgentID: "main", Phase: "", StartedAt: now},
	}, now)

	if !strings.Contains(got, "1 main task, no subagents") {
		t.Fatalf("missing summary line, got:\n%s", got)
	}
	if !strings.Contains(got, "Main task · agent: main · working · 0s") {
		t.Fatalf("missing main task line, got:\n%s", got)
	}
	if strings.Contains(got, "Subagent 1") {
		t.Fatalf("unexpected subagent line, got:\n%s", got)
	}
}

func TestCompactPreview_Truncates(t *testing.T) {
	long := strings.Repeat("à", 100)
	got := compactPreview(long, subagentPreviewMaxRunes)
	if n := len([]rune(got)); n != subagentPreviewMaxRunes {
		t.Fatalf("preview rune len=%d, want=%d", n, subagentPreviewMaxRunes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("preview=%q, want ellipsis suffix", got)
	}
}

func TestSubagentsCommand_Handler(t *testing.T) {
	rt := &Runtime{
		GetActiveTurnTree: func() []TurnInfo {
			return []TurnInfo{{TurnID: "root", AgentID: "main", Phase: "running", StartedAt: time.Now()}}
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Channel: "telegram",
		Text:    "/subagents",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("/subagents outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	if !strings.Contains(reply, "Main task · agent: main · thinking") {
		t.Fatalf("/subagents reply=%q, expected friendly tree", reply)
	}
	if strings.Contains(reply, "TurnID:") || strings.Contains(reply, "&{") {
		t.Fatalf("/subagents reply leaks raw struct dump: %q", reply)
	}
}
