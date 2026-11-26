package session_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestJSONLBackendConcurrentWritesPreserveAllMessages(t *testing.T) {
	b := newBackend(t)
	const writers = 64
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- b.AddMessage(
				context.Background(),
				"concurrent-session",
				"user",
				fmt.Sprintf("message-%d", i),
			)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("AddMessage() error = %v", err)
		}
	}

	history := mustGetHistory(t, b, "concurrent-session")
	if len(history) != writers {
		t.Fatalf("history length = %d, want %d", len(history), writers)
	}
	seen := make(map[string]bool, len(history))
	for _, message := range history {
		seen[message.Content] = true
	}
	for i := range writers {
		content := fmt.Sprintf("message-%d", i)
		if !seen[content] {
			t.Fatalf("history is missing %q", content)
		}
	}
}
