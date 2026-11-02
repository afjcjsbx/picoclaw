package tools

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// planTool is a ParallelAware test tool. Its safety comes from the call:
// "unsafe" marks a barrier, "r" and "w" are the paths read and written.
type planTool struct{ name string }

func (p planTool) Name() string        { return p.name }
func (p planTool) Description() string { return "plan test tool" }
func (p planTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (p planTool) Execute(context.Context, map[string]any) *ToolResult { return NewToolResult("ok") }

func (p planTool) ParallelSafety(args map[string]any) ParallelSafety {
	s := ParallelSafety{Safe: args["unsafe"] == nil}
	if r, ok := args["r"].(string); ok {
		s.Paths = append(s.Paths, PathAccess{Path: r})
	}
	if w, ok := args["w"].(string); ok {
		s.Paths = append(s.Paths, PathAccess{Path: w, Write: true})
	}
	return s
}

// plainTool does not implement ParallelAware.
type plainTool struct{ name string }

func (p plainTool) Name() string        { return p.name }
func (p plainTool) Description() string { return "plain test tool" }
func (p plainTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (p plainTool) Execute(context.Context, map[string]any) *ToolResult { return NewToolResult("ok") }

func formatSegments(segments []Segment) string {
	parts := make([]string, 0, len(segments))
	for _, s := range segments {
		kind := "S"
		if s.Parallel {
			kind = "P"
		}
		parts = append(parts, fmt.Sprintf("%s%v", kind, s.Calls))
	}
	return strings.Join(parts, " ")
}

func TestPlanBatch(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(planTool{name: "net"})
	reg.Register(planTool{name: "fs"})
	reg.Register(plainTool{name: "exec"})

	net := providers.ToolCall{Name: "net", Arguments: map[string]any{}}
	exec := providers.ToolCall{Name: "exec", Arguments: map[string]any{}}
	unknown := providers.ToolCall{Name: "missing", Arguments: map[string]any{}}
	barrier := providers.ToolCall{Name: "fs", Arguments: map[string]any{"unsafe": true}}
	read := func(p string) providers.ToolCall {
		return providers.ToolCall{Name: "fs", Arguments: map[string]any{"r": p}}
	}
	write := func(p string) providers.ToolCall {
		return providers.ToolCall{Name: "fs", Arguments: map[string]any{"w": p}}
	}

	tests := []struct {
		name  string
		calls []providers.ToolCall
		want  string
	}{
		{"empty", nil, ""},
		{"single safe call is sequential", []providers.ToolCall{net}, "S[0]"},
		{"two safe calls", []providers.ToolCall{net, net}, "P[0 1]"},
		{"barrier splits groups", []providers.ToolCall{net, net, exec, net, net}, "P[0 1] S[2] P[3 4]"},
		{"adjacent barriers merge", []providers.ToolCall{exec, exec, net, net}, "S[0 1] P[2 3]"},
		{"lone group between barriers merges", []providers.ToolCall{exec, net, exec}, "S[0 1 2]"},
		{"unknown tool is a barrier", []providers.ToolCall{net, unknown, net, net}, "S[0 1] P[2 3]"},
		{"tool flagging a call unsafe is a barrier", []providers.ToolCall{net, barrier, net, net}, "S[0 1] P[2 3]"},
		{"reads never conflict", []providers.ToolCall{read("/w/a"), read("/w/a"), read("/w/b")}, "P[0 1 2]"},
		{"read then write same path", []providers.ToolCall{read("/w/a"), write("/w/a")}, "S[0 1]"},
		{"two writes same path", []providers.ToolCall{write("/w/a"), write("/w/a")}, "S[0 1]"},
		{"writes on different paths", []providers.ToolCall{write("/w/a"), write("/w/b")}, "P[0 1]"},
		{
			"collision closes the group and opens the next",
			[]providers.ToolCall{read("/w/a"), read("/w/b"), write("/w/a"), read("/w/c")},
			"P[0 1] P[2 3]",
		},
		{
			"spec example with barrier",
			[]providers.ToolCall{net, read("/w/a"), exec, net, read("/w/b"), write("/w/a")},
			"P[0 1] S[2] P[3 4 5]",
		},
		{"directory contains file", []providers.ToolCall{read("/w"), write("/w/x")}, "S[0 1]"},
		{"file inside directory", []providers.ToolCall{write("/w/x"), read("/w")}, "S[0 1]"},
		{"sibling with shared prefix", []providers.ToolCall{write("/w/a"), write("/w/ab")}, "P[0 1]"},
		{"case-insensitive", []providers.ToolCall{read("/W/A"), write("/w/a")}, "S[0 1]"},
		{"root contains everything", []providers.ToolCall{read("/"), write("/w/a")}, "S[0 1]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			segments := PlanBatch(reg, tt.calls)
			if got := formatSegments(segments); got != tt.want {
				t.Fatalf("PlanBatch = %q, want %q", got, tt.want)
			}
			next := 0
			for _, s := range segments {
				if s.Parallel && len(s.Calls) < 2 {
					t.Errorf("parallel segment with %d calls", len(s.Calls))
				}
				for _, idx := range s.Calls {
					if idx != next {
						t.Fatalf("segments reorder the batch: got index %d, want %d", idx, next)
					}
					next++
				}
			}
			if next != len(tt.calls) {
				t.Fatalf("segments cover %d of %d calls", next, len(tt.calls))
			}
		})
	}
}

func TestPlanBatch_NilRegistryIsSequential(t *testing.T) {
	calls := []providers.ToolCall{{Name: "net"}, {Name: "net"}}
	if got := formatSegments(PlanBatch(nil, calls)); got != "S[0 1]" {
		t.Fatalf("PlanBatch(nil) = %q, want S[0 1]", got)
	}
}

// RunToolLoop runs sequential segments with limit 1 and relies on it keeping
// the call order and surviving a panic in the middle.
func TestRunConcurrent_LimitOneRunsInOrderAndRecovers(t *testing.T) {
	var order []int // no lock: limit 1 must never overlap calls
	RunConcurrent(5, 1, func(i int) {
		order = append(order, i)
		if i == 2 {
			panic("boom")
		}
	})
	if want := []int{0, 1, 2, 3, 4}; !slices.Equal(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestRunConcurrent_BoundsAndRecovers(t *testing.T) {
	var active, maxSeen, ran atomic.Int32
	var mu sync.Mutex
	RunConcurrent(12, 3, func(i int) {
		n := active.Add(1)
		mu.Lock()
		maxSeen.Store(max(maxSeen.Load(), n))
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		active.Add(-1)
		ran.Add(1)
		if i == 5 {
			panic("boom")
		}
	})
	if ran.Load() != 12 {
		t.Errorf("ran = %d, want 12 (a panic must not stop the others)", ran.Load())
	}
	if maxSeen.Load() > 3 || maxSeen.Load() < 2 {
		t.Errorf("max concurrent = %d, want 2..3", maxSeen.Load())
	}
}
