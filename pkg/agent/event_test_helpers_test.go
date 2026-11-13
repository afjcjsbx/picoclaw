package agent

import (
	"testing"
	"time"
)

func waitForEvent(t *testing.T, ch <-chan Event, timeout time.Duration, match func(Event) bool) Event {
	t.Helper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				t.Fatal("event channel closed")
			}
			if match == nil || match(evt) {
				return evt
			}
		case <-timer.C:
			t.Fatal("timed out waiting for event")
		}
	}
}
