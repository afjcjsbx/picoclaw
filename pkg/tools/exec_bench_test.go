package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func BenchmarkExecRunTrue(b *testing.B) {
	ws := b.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = ws
	cfg.Approvals = config.ApprovalsConfig{Mode: config.ApprovalModeOff}
	tool, err := NewExecToolWithConfig(ws, true, cfg)
	if err != nil {
		b.Fatal(err)
	}
	ctx := WithToolContext(context.Background(), "cli", "bench")
	ctx = WithToolSessionContext(ctx, "bench-agent", "bench-session", nil)
	for i := 0; i < 5; i++ {
		_ = tool.Execute(ctx, map[string]any{"action": "run", "command": "true"})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := tool.Execute(ctx, map[string]any{"action": "run", "command": "true"})
		if res.IsError {
			b.Fatal(res.ForLLM)
		}
	}
}

func BenchmarkExecGuard500(b *testing.B) {
	ws := b.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = ws
	cfg.Approvals = config.ApprovalsConfig{Mode: config.ApprovalModeOff}
	tool, err := NewExecToolWithConfig(ws, true, cfg)
	if err != nil {
		b.Fatal(err)
	}
	ctx := WithToolContext(context.Background(), "cli", "bench")
	command := "echo " + strings.Repeat("arg ", 500)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tool.guardCommandContext(ctx, command, ws)
	}
}
