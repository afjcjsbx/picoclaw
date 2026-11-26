//go:build no_seahorse || mipsle || netbsd || (freebsd && arm)

package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

func deleteSeahorseSession(_ context.Context, sessionsDir, _ string) error {
	dbPath := filepath.Join(sessionsDir, "seahorse.db")
	if _, err := os.Stat(dbPath); err == nil {
		return fmt.Errorf("seahorse database exists but Seahorse support is not included in this build")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat seahorse database: %w", err)
	}
	return nil
}
