//go:build !no_seahorse && !mipsle && !netbsd && !(freebsd && arm)

package api

import (
	"context"
	"path/filepath"

	"github.com/sipeed/picoclaw/pkg/seahorse"
)

func deleteSeahorseSession(ctx context.Context, sessionsDir, sessionKey string) error {
	return seahorse.DeleteSessionFromDB(ctx, filepath.Join(sessionsDir, "seahorse.db"), sessionKey)
}
