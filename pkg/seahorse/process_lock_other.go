//go:build !unix

package seahorse

// acquireProcessLock is a no-op on platforms without a portable advisory file
// lock. The database still relies on SQLite's own locking, but multi-process
// operation should be avoided there.
func acquireProcessLock(string) (func() error, error) {
	return func() error { return nil }, nil
}
