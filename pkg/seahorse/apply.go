package seahorse

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// ApplyLeafInput is the atomic apply of one leaf summary. The summary content
// and metadata are produced outside the transaction (after the LLM call); the
// transaction verifies that the candidate messages and context generation are
// still the ones the compactor selected, then writes the summary, its source
// links and the context replacement together.
type ApplyLeafInput struct {
	ConversationID  int64
	ExpectedVersion int64
	StartOrdinal    int
	EndOrdinal      int
	MessageIDs      []int64
	Summary         CreateSummaryInput
}

// ApplyCondensedInput is the atomic apply of one condensed summary.
type ApplyCondensedInput struct {
	ConversationID      int64
	ExpectedVersion     int64
	CandidateSummaryIDs []string
	// InsertBeforeOrdinal is the last candidate ordinal. The new summary is
	// placed there after candidates are deleted so it stays after any skipped
	// oversized item that sits between candidates.
	InsertBeforeOrdinal int
	Summary             CreateSummaryInput
}

// ApplyLeafSummary atomically commits a leaf summary. It returns
// ErrStaleCandidates without creating any summary when the context changed.
func (s *Store) ApplyLeafSummary(ctx context.Context, in ApplyLeafInput) (*Summary, error) {
	var summary *Summary
	err := retryBusy(ctx, func() error {
		var err error
		summary, err = s.applyLeafSummaryOnce(ctx, in)
		return err
	})
	return summary, err
}

func (s *Store) applyLeafSummaryOnce(ctx context.Context, in ApplyLeafInput) (*Summary, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	version, err := getConversationVersionTx(ctx, tx, in.ConversationID)
	if err != nil {
		return nil, err
	}
	if version != in.ExpectedVersion {
		return nil, ErrStaleCandidates
	}

	if verifyErr := verifyLeafCandidatesTx(ctx, tx, in); verifyErr != nil {
		return nil, verifyErr
	}

	in.Summary.ConversationID = in.ConversationID
	summary, err := createSummaryTx(ctx, tx, in.Summary)
	if err != nil {
		return nil, err
	}
	if err := linkSummaryToMessagesTx(ctx, tx, summary.SummaryID, in.MessageIDs); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx,
		"DELETE FROM context_items WHERE conversation_id = ? AND ordinal >= ? AND ordinal <= ?",
		in.ConversationID, in.StartOrdinal, in.EndOrdinal,
	); err != nil {
		return nil, err
	}
	if err := insertSummaryContextItemTx(
		ctx, tx, in.ConversationID, summary.SummaryID, in.StartOrdinal, in.StartOrdinal,
	); err != nil {
		return nil, err
	}
	if err := bumpConversationVersionTx(ctx, tx, in.ConversationID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return summary, nil
}

// ApplyCondensedSummary atomically commits a condensed summary. It verifies
// that every candidate is still present before the summary row is created.
func (s *Store) ApplyCondensedSummary(
	ctx context.Context,
	in ApplyCondensedInput,
) (*Summary, error) {
	if len(in.CandidateSummaryIDs) == 0 {
		return nil, ErrStaleCandidates
	}
	var summary *Summary
	err := retryBusy(ctx, func() error {
		var err error
		summary, err = s.applyCondensedSummaryOnce(ctx, in)
		return err
	})
	return summary, err
}

func (s *Store) applyCondensedSummaryOnce(
	ctx context.Context,
	in ApplyCondensedInput,
) (*Summary, error) {
	if len(in.CandidateSummaryIDs) == 0 {
		return nil, ErrStaleCandidates
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	version, err := getConversationVersionTx(ctx, tx, in.ConversationID)
	if err != nil {
		return nil, err
	}
	if version != in.ExpectedVersion {
		return nil, ErrStaleCandidates
	}
	if verifyErr := verifyCondensedCandidatesTx(ctx, tx, in); verifyErr != nil {
		return nil, verifyErr
	}

	in.Summary.ConversationID = in.ConversationID
	summary, err := createSummaryTx(ctx, tx, in.Summary)
	if err != nil {
		return nil, err
	}

	for _, summaryID := range in.CandidateSummaryIDs {
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM context_items WHERE conversation_id = ? AND summary_id = ?",
			in.ConversationID, summaryID,
		); err != nil {
			return nil, err
		}
	}

	insertBefore := in.InsertBeforeOrdinal
	if insertBefore <= 0 {
		insertBefore = 1
	}
	if err := insertSummaryContextItemTx(
		ctx, tx, in.ConversationID, summary.SummaryID, insertBefore, insertBefore,
	); err != nil {
		return nil, err
	}
	if err := bumpConversationVersionTx(ctx, tx, in.ConversationID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return summary, nil
}

func verifyLeafCandidatesTx(ctx context.Context, tx *sql.Tx, in ApplyLeafInput) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT ordinal, message_id FROM context_items
		 WHERE conversation_id = ? AND ordinal >= ? AND ordinal <= ?
		 ORDER BY ordinal`,
		in.ConversationID, in.StartOrdinal, in.EndOrdinal,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var ordinal int
		var messageID sql.NullInt64
		if err := rows.Scan(&ordinal, &messageID); err != nil {
			return err
		}
		if !messageID.Valid {
			return ErrStaleCandidates
		}
		ids = append(ids, messageID.Int64)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ids) != len(in.MessageIDs) {
		return ErrStaleCandidates
	}
	for i := range ids {
		if ids[i] != in.MessageIDs[i] {
			return ErrStaleCandidates
		}
	}
	return nil
}

func verifyCondensedCandidatesTx(ctx context.Context, tx *sql.Tx, in ApplyCondensedInput) error {
	seen := make(map[string]struct{}, len(in.CandidateSummaryIDs))
	for _, id := range in.CandidateSummaryIDs {
		if id == "" {
			return ErrStaleCandidates
		}
		if _, duplicate := seen[id]; duplicate {
			return ErrStaleCandidates
		}
		seen[id] = struct{}{}
	}

	rows, err := tx.QueryContext(ctx,
		`SELECT summary_id FROM context_items
		 WHERE conversation_id = ? AND item_type = 'summary' AND summary_id IN `+
			sqlInPlaceholders(len(in.CandidateSummaryIDs)),
		append([]any{in.ConversationID}, stringsToAny(in.CandidateSummaryIDs)...)...,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	found := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if _, ok := seen[id]; ok {
			found++
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if found != len(seen) {
		return ErrStaleCandidates
	}
	return nil
}

// createSummaryTx is the transaction-scoped implementation of CreateSummary.
func createSummaryTx(ctx context.Context, tx *sql.Tx, input CreateSummaryInput) (*Summary, error) {
	now := time.Now().UTC()
	summaryID := generateSummaryID(input.Content, now)

	var earliestAt, latestAt sql.NullString
	if input.EarliestAt != nil {
		earliestAt = sql.NullString{String: input.EarliestAt.Format(time.RFC3339), Valid: true}
	}
	if input.LatestAt != nil {
		latestAt = sql.NullString{String: input.LatestAt.Format(time.RFC3339), Valid: true}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO summaries (summary_id, conversation_id, kind, depth, content, token_count,
			earliest_at, latest_at, descendant_count, descendant_token_count,
			source_message_token_count, model)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		summaryID, input.ConversationID, string(input.Kind), input.Depth,
		input.Content, input.TokenCount,
		earliestAt, latestAt,
		input.DescendantCount, input.DescendantTokenCount,
		input.SourceMessageTokens, input.Model,
	); err != nil {
		return nil, fmt.Errorf("insert summary: %w", err)
	}

	for _, parentID := range input.ParentIDs {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO summary_parents (summary_id, parent_summary_id) VALUES (?, ?)",
			summaryID, parentID,
		); err != nil {
			return nil, fmt.Errorf("link parent %s: %w", parentID, err)
		}
	}

	return &Summary{
		SummaryID:               summaryID,
		ConversationID:          input.ConversationID,
		Kind:                    input.Kind,
		Depth:                   input.Depth,
		Content:                 input.Content,
		TokenCount:              input.TokenCount,
		EarliestAt:              input.EarliestAt,
		LatestAt:                input.LatestAt,
		DescendantCount:         input.DescendantCount,
		DescendantTokenCount:    input.DescendantTokenCount,
		SourceMessageTokenCount: input.SourceMessageTokens,
		Model:                   input.Model,
		CreatedAt:               now,
	}, nil
}

func linkSummaryToMessagesTx(
	ctx context.Context,
	tx *sql.Tx,
	summaryID string,
	messageIDs []int64,
) error {
	for i, msgID := range messageIDs {
		if _, err := tx.ExecContext(ctx,
			"INSERT OR IGNORE INTO summary_messages (summary_id, message_id, ordinal) VALUES (?, ?, ?)",
			summaryID, msgID, i,
		); err != nil {
			return fmt.Errorf("link message %d: %w", msgID, err)
		}
	}
	return nil
}

// insertSummaryContextItemTx tries the requested ordinal and falls back to a
// chronology-preserving resequence when another item already occupies it.
func insertSummaryContextItemTx(
	ctx context.Context, tx *sql.Tx, convID int64, summaryID string, desired, insertBefore int,
) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO context_items (conversation_id, ordinal, item_type, summary_id, token_count)
		 SELECT ?, ?, 'summary', ?, token_count FROM summaries WHERE summary_id = ?`,
		convID, desired, summaryID, summaryID,
	)
	if err == nil {
		return nil
	}
	if !isUniqueViolation(err) {
		return err
	}
	return resequenceContextItemsWithSummaryTx(ctx, tx, convID, summaryID, insertBefore)
}

// resequenceContextItemsWithSummaryTx rebuilds the ordinal space and inserts
// the new summary at the position determined by insertBefore, preserving
// chronological order. The previous implementation appended the summary at the
// end, which could move an old summary into the fresh tail.
func resequenceContextItemsWithSummaryTx(
	ctx context.Context, tx *sql.Tx, convID int64, summaryID string, insertBefore int,
) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT ordinal, item_type, summary_id, message_id, token_count
		 FROM context_items WHERE conversation_id = ? ORDER BY ordinal`, convID)
	if err != nil {
		return err
	}
	defer rows.Close()

	type item struct {
		ordinal    int
		itemType   string
		summaryID  string
		messageID  int64
		tokenCount int
	}
	var items []item
	for rows.Next() {
		var it item
		var sid sql.NullString
		var mid sql.NullInt64
		if err := rows.Scan(&it.ordinal, &it.itemType, &sid, &mid, &it.tokenCount); err != nil {
			return err
		}
		if sid.Valid {
			it.summaryID = sid.String
		}
		if mid.Valid {
			it.messageID = mid.Int64
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	insertion := sort.Search(len(items), func(i int) bool {
		return items[i].ordinal >= insertBefore
	})

	var summaryTokens int
	if err := tx.QueryRowContext(ctx,
		"SELECT token_count FROM summaries WHERE summary_id = ?", summaryID,
	).Scan(&summaryTokens); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx,
		"DELETE FROM context_items WHERE conversation_id = ?", convID); err != nil {
		return err
	}

	ordinal := OrdinalStep
	for i := 0; i <= len(items); i++ {
		if i == insertion {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO context_items (conversation_id, ordinal, item_type, summary_id, token_count)
				 VALUES (?, ?, 'summary', ?, ?)`,
				convID, ordinal, summaryID, summaryTokens,
			); err != nil {
				return err
			}
			ordinal += OrdinalStep
		}
		if i == len(items) {
			break
		}
		it := items[i]
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO context_items (conversation_id, ordinal, item_type, summary_id, message_id, token_count)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			convID, ordinal, it.itemType, nullString(it.summaryID), nullInt64(it.messageID), it.tokenCount,
		); err != nil {
			return err
		}
		ordinal += OrdinalStep
	}
	return nil
}

func stringsToAny(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}
