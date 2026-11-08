package seahorse

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"time"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	writeTxMaxAttempts = 4
	writeTxBaseBackoff = 20 * time.Millisecond
)

func sleepBackoff(ctx context.Context, attempt int) error {
	backoff := writeTxBaseBackoff << attempt
	// Small jitter prevents two waiters from retrying in lockstep.
	backoff += time.Duration(rand.Int63n(int64(writeTxBaseBackoff)))
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retryBusy retries a self-contained write operation from the beginning when
// it fails with a transient SQLite lock error. The callback must open and
// close its own transaction.
func retryBusy(ctx context.Context, fn func() error) error {
	var err error
	for attempt := 0; attempt < writeTxMaxAttempts; attempt++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		err = fn()
		if err == nil || !isBusyError(err) || attempt+1 >= writeTxMaxAttempts {
			return err
		}
		if backoffErr := sleepBackoff(ctx, attempt); backoffErr != nil {
			return backoffErr
		}
	}
	return err
}

func isBusyError(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		code := sqliteErr.Code()
		return code == sqlite3.SQLITE_BUSY ||
			code == sqlite3.SQLITE_BUSY_SNAPSHOT ||
			code == sqlite3.SQLITE_LOCKED ||
			(code&0xff) == sqlite3.SQLITE_BUSY ||
			(code&0xff) == sqlite3.SQLITE_LOCKED
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked")
}

func isSQLiteCode(err error, codes ...int) bool {
	if err == nil {
		return false
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		for _, code := range codes {
			if sqliteErr.Code() == code {
				return true
			}
		}
	}
	return false
}
