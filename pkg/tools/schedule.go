package tools

import (
	"fmt"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// Segment is a run of calls of one LLM response that executes together
// (Parallel) or one by one. Calls holds indexes into the original batch, in
// original order.
type Segment struct {
	Parallel bool
	Calls    []int
}

// PlanBatch splits the tool calls of one LLM response into segments that
// execute in order. It never reorders calls: a parallel segment only groups
// consecutive calls that declare themselves safe (ParallelAware) and whose
// filesystem footprints do not conflict. Unknown tools, tools that do not
// implement ParallelAware and calls they flag unsafe are sequential barriers.
// A parallel group of a single call is demoted to sequential.
func PlanBatch(reg *ToolRegistry, calls []providers.ToolCall) []Segment {
	var segments []Segment
	var group []int
	var reserved []PathAccess

	appendSequential := func(i int) {
		if n := len(segments); n > 0 && !segments[n-1].Parallel {
			segments[n-1].Calls = append(segments[n-1].Calls, i)
			return
		}
		segments = append(segments, Segment{Calls: []int{i}})
	}
	flush := func() {
		switch len(group) {
		case 0:
		case 1:
			appendSequential(group[0])
		default:
			segments = append(segments, Segment{Parallel: true, Calls: group})
		}
		group, reserved = nil, nil
	}

	for i, call := range calls {
		safety := callSafety(reg, call)
		if !safety.Safe {
			flush()
			appendSequential(i)
			continue
		}
		if pathsConflict(reserved, safety.Paths) {
			flush()
		}
		group = append(group, i)
		reserved = append(reserved, safety.Paths...)
	}
	flush()
	return segments
}

func callSafety(reg *ToolRegistry, call providers.ToolCall) ParallelSafety {
	if reg == nil {
		return ParallelSafety{}
	}
	tool, ok := reg.Get(call.Name)
	if !ok {
		return ParallelSafety{}
	}
	aware, ok := tool.(ParallelAware)
	if !ok {
		return ParallelSafety{}
	}
	return aware.ParallelSafety(call.Arguments)
}

func pathsConflict(reserved, paths []PathAccess) bool {
	for _, a := range paths {
		for _, b := range reserved {
			if (a.Write || b.Write) && pathsOverlap(a.Path, b.Path) {
				return true
			}
		}
	}
	return false
}

// pathsOverlap reports whether the paths are equal or one contains the other.
// The comparison is case-insensitive on every platform: a false conflict only
// costs parallelism, a missed one on a case-insensitive filesystem corrupts data.
func pathsOverlap(a, b string) bool {
	a = strings.ToLower(filepath.Clean(a))
	b = strings.ToLower(filepath.Clean(b))
	return a == b || pathWithin(a, b) || pathWithin(b, a)
}

func pathWithin(path, dir string) bool {
	return strings.HasPrefix(path, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

// RunConcurrent calls fn(0..n-1) with at most limit calls in flight and waits
// for all of them. A panic in fn is recovered and logged, leaving the caller's
// result slot for that index untouched.
func RunConcurrent(n, limit int, fn func(i int)) {
	limit = max(limit, 1)
	var wg sync.WaitGroup
	sem := make(chan struct{}, limit)
	for i := range n {
		// Block until a slot is free: the call count comes from the model and
		// each call may spawn a process or open network connections.
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					logger.ErrorCF("tool", "concurrent tool goroutine panic recovered",
						map[string]any{"index": i, "panic": fmt.Sprint(r), "stack": string(debug.Stack())})
				}
			}()
			fn(i)
		}()
	}
	wg.Wait()
}
