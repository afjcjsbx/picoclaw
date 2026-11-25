package seahorse

import (
	"context"
	"database/sql"
	"fmt"
)

// GCOrphans removes rows that can no longer be resolved. It is deliberately
// conservative: summaries that are not currently in context are kept because
// short_grep can still retrieve them, but rows whose parent/source/context
// target is missing are removed.
func (s *Store) GCOrphans(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := gcOrphansTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// gcOrphansTx removes rows that cannot be resolved. Parents are cleaned before
// context items so items whose message or summary is removed in this pass are
// also deleted.
func gcOrphansTx(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`DELETE FROM messages
		 WHERE NOT EXISTS (SELECT 1 FROM conversations c WHERE c.conversation_id = messages.conversation_id)`,
		`DELETE FROM summaries
		 WHERE NOT EXISTS (SELECT 1 FROM conversations c WHERE c.conversation_id = summaries.conversation_id)`,
		`DELETE FROM summary_messages
		 WHERE NOT EXISTS (SELECT 1 FROM summaries su WHERE su.summary_id = summary_messages.summary_id)
		    OR NOT EXISTS (SELECT 1 FROM messages m WHERE m.message_id = summary_messages.message_id)`,
		`DELETE FROM summary_parents
		 WHERE NOT EXISTS (SELECT 1 FROM summaries su WHERE su.summary_id = summary_parents.summary_id)
		    OR NOT EXISTS (SELECT 1 FROM summaries p WHERE p.summary_id = summary_parents.parent_summary_id)`,
		`DELETE FROM message_parts
		 WHERE NOT EXISTS (SELECT 1 FROM messages m WHERE m.message_id = message_parts.message_id)`,
		`DELETE FROM context_items
		 WHERE NOT EXISTS (SELECT 1 FROM conversations c WHERE c.conversation_id = context_items.conversation_id)
		    OR (item_type = 'message' AND NOT EXISTS (
			SELECT 1 FROM messages m WHERE m.message_id = context_items.message_id))
		    OR (item_type = 'summary' AND NOT EXISTS (
			SELECT 1 FROM summaries su WHERE su.summary_id = context_items.summary_id))`,
	}
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("gc orphans: %w", err)
		}
	}
	return nil
}
