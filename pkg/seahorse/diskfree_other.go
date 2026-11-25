//go:build !unix

package seahorse

// diskFreeBytes is unavailable on this platform. Callers must not treat the
// missing value as "no space"; VACUUM still lets SQLite report its own error.
func diskFreeBytes(string) (int64, bool) {
	return 0, false
}
