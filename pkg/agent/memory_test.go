// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// TestAppendTodayConcurrentWrites verifies that concurrent appends to today's
// daily note are serialized rather than racing on the read-modify-write cycle.
//
// Before the fix, AppendToday performed os.ReadFile -> concatenate ->
// atomic write without any lock. Two goroutines reading the same snapshot
// would produce a lost update when their renames landed in sequence.
func TestAppendTodayConcurrentWrites(t *testing.T) {
	ms := NewMemoryStore(t.TempDir())

	const writers = 50
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := range writers {
		go func(i int) {
			defer wg.Done()
			if err := ms.AppendToday(fmt.Sprintf("entry-%d", i)); err != nil {
				t.Errorf("AppendToday: %v", err)
			}
		}(i)
	}
	wg.Wait()

	content := ms.ReadToday()
	if got := strings.Count(content, "entry-"); got != writers {
		t.Fatalf("lost updates: expected %d entries, observed %d\ncontent:\n%s", writers, got, content)
	}
}
