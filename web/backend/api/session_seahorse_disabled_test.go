//go:build no_seahorse || mipsle || netbsd || (freebsd && arm)

package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteSeahorseSessionDisabledFailsClosedForExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	if err := deleteSeahorseSession(context.Background(), dir, "session:missing"); err != nil {
		t.Fatalf("missing database should be a no-op: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seahorse.db"), []byte("residual data"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := deleteSeahorseSession(context.Background(), dir, "session:missing"); err == nil {
		t.Fatal("expected an error for an existing Seahorse database in a build without Seahorse support")
	}
}
