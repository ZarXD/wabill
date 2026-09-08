package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type DeduplicationRepo struct {
	db *DB
}

func NewDeduplicationRepo(db *DB) *DeduplicationRepo {
	return &DeduplicationRepo{db: db}
}

// TryAcquireMessageLock attempts to insert the message ID.
// If the message was already processed (already exists in the table), returns false.
// If successfully inserted, returns true (safe to process).
func (r *DeduplicationRepo) TryAcquireMessageLock(ctx context.Context, messageID string) (bool, error) {
	if messageID == "" {
		return true, nil
	}

	query := `
		INSERT INTO processed_events (message_id, processed_at)
		VALUES ($1, NOW())
		ON CONFLICT (message_id) DO NOTHING
		RETURNING message_id
	`
	var returnedID string
	err := r.db.QueryRowContext(ctx, query, messageID).Scan(&returnedID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Duplicate! Already exists
			return false, nil
		}
		return false, fmt.Errorf("failed to check message deduplication: %w", err)
	}
	return true, nil
}
