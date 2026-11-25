package hardwaretools

import (
	"testing"
	"time"
)

func TestLockI2CBusSerializesSameBus(t *testing.T) {
	t.Parallel()

	unlock := lockI2CBus("test-same-bus")
	acquired := make(chan struct{})
	go func() {
		nextUnlock := lockI2CBus("test-same-bus")
		close(acquired)
		nextUnlock()
	}()

	select {
	case <-acquired:
		unlock()
		t.Fatal("second lock acquired while the bus was held")
	case <-time.After(50 * time.Millisecond):
	}

	unlock()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second lock was not released after the first unlock")
	}
}

func TestLockI2CBusAllowsDifferentBuses(t *testing.T) {
	t.Parallel()

	unlockA := lockI2CBus("test-bus-a")
	defer unlockA()

	acquired := make(chan struct{})
	go func() {
		unlockB := lockI2CBus("test-bus-b")
		close(acquired)
		unlockB()
	}()

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("different buses blocked each other")
	}
}
