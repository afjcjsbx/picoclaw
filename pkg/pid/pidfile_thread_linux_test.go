//go:build linux

package pid

import (
	"os"
	"runtime"
	"syscall"
	"testing"
)

// TestIsProcessRunningRejectsThreadPid verifies that a non-leader thread ID
// (Tgid != Pid) is not mistaken for a running process. A stale PID file from
// a previous boot can match a thread ID of the live gateway, which used to
// block every gateway restart with a false "already running" error.
func TestIsProcessRunningRejectsThreadPid(t *testing.T) {
	release := make(chan struct{})
	tids := make(chan int, 1)
	go func() {
		runtime.LockOSThread()
		tids <- syscall.Gettid()
		<-release
	}()
	tid := <-tids
	defer close(release)

	if tid == os.Getpid() {
		t.Skip("thread ID equals process ID; cannot exercise thread check")
	}
	if isProcessRunning(tid) {
		t.Fatalf("isProcessRunning(%d) = true for a thread ID, want false", tid)
	}
	if !isProcessRunning(os.Getpid()) {
		t.Fatal("isProcessRunning(self) = false, want true")
	}
}
