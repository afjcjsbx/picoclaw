package auth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"github.com/sipeed/picoclaw/pkg/config"
)

// authStoreLockRetryHook is used by tests to observe lock contention without
// relying on scheduler timing. It is nil in production.
var authStoreLockRetryHook func()

// withAuthStoreFileLock serializes auth.json read-modify-write operations
// across PicoClaw processes. The in-process mutex in store.go is still needed
// because flock ownership is tied to file descriptors, not goroutines.
func withAuthStoreFileLock(ctx context.Context, fn func() error) error {
	home := config.GetHome()
	if err := os.MkdirAll(home, 0o700); err != nil {
		return fmt.Errorf("create auth store directory: %w", err)
	}
	lock := flock.New(filepath.Join(home, "auth.lock"))
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("lock auth store: %w", err)
		}
		locked, err := lock.TryLock()
		if err != nil {
			return fmt.Errorf("lock auth store: %w", err)
		}
		if locked {
			break
		}
		if authStoreLockRetryHook != nil {
			authStoreLockRetryHook()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("lock auth store: %w", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer func() { _ = lock.Unlock() }()
	return fn()
}
