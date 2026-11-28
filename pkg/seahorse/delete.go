package seahorse

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
)

// DeleteSessionFromDB removes a session from an existing Seahorse database
// without acquiring the engine process lock. The Web launcher uses this one-shot
// operation while the gateway may own the engine lock; an immediate SQLite
// transaction keeps the lookup and delete atomic with respect to gateway writes.
func DeleteSessionFromDB(ctx context.Context, dbPath, sessionKey string) error {
	if strings.TrimSpace(sessionKey) == "" {
		return nil
	}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat seahorse database: %w", err)
	}

	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return fmt.Errorf("open seahorse database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer db.Close()

	store := &Store{db: db}
	if err := store.deleteSessionByKey(ctx, sessionKey); err != nil {
		return fmt.Errorf("delete seahorse conversation: %w", err)
	}
	return nil
}

func (s *Store) deleteSessionByKey(ctx context.Context, sessionKey string) error {
	return retryBusy(ctx, func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()

		var convID int64
		err = tx.QueryRowContext(ctx,
			"SELECT conversation_id FROM conversations WHERE session_key = ?",
			sessionKey,
		).Scan(&convID)
		if err == sql.ErrNoRows {
			return tx.Commit()
		}
		if err != nil {
			return fmt.Errorf("find conversation: %w", err)
		}
		if err := clearConversationTx(ctx, tx, convID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM conversations WHERE conversation_id = ?",
			convID,
		); err != nil {
			return fmt.Errorf("delete conversation row: %w", err)
		}
		return tx.Commit()
	})
}
