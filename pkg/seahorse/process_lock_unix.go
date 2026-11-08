//go:build unix

package seahorse

import (
	"fmt"
	"os"
	"syscall"
)

// acquireProcessLock takes an advisory exclusive lock so two PicoClaw
// processes cannot write the same Seahorse database concurrently. The lock is
// tied to the open file descriptor and disappears automatically on process
// exit, so a crash does not leave a permanently stale lock.
func acquireProcessLock(dbPath string) (func() error, error) {
	if dbPath == "" || dbPath == ":memory:" {
		return func() error { return nil }, nil
	}
	f, err := os.OpenFile(dbPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open seahorse lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("seahorse database is already open by another process: %w", err)
	}
	return func() error {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return f.Close()
	}, nil
}
