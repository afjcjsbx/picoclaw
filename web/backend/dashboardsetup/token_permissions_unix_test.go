//go:build !windows

package dashboardsetup

import (
	"os"
	"testing"
)

func assertTokenPermissions(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("token file permissions = %o, want 600", got)
	}
}
