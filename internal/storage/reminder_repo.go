package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"wabill/internal/domain"
)

type ReminderRepo struct {
	db *DB
}

func NewReminderRepo(db *DB) *ReminderRepo {
	return &ReminderRepo{db: db}
}

func (r *ReminderRepo) HasReminderBeenSent(ctx context.Context, subscriptionID int64, period, reminderType string) (bool, error) {
	query := `
		SELECT id
		FROM reminders
		WHERE subscription_id = $1 AND billing_period = $2 AND reminder_type = $3
		LIMIT 1
	`
	var id int64
	err := r.db.QueryRowContext(ctx, query, subscriptionID, period, reminderType).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check reminder: %w", err)
	}
	return true, nil
}

func (r *ReminderRepo) RecordReminder(ctx context.Context, rem *domain.Reminder) error {
	query := `
		INSERT INTO reminders (subscription_id, billing_period, reminder_type, sent_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (subscription_id, billing_period, reminder_type) DO NOTHING
		RETURNING id, sent_at
	`
	err := r.db.QueryRowContext(ctx, query, rem.SubscriptionID, rem.BillingPeriod, rem.ReminderType).
		Scan(&rem.ID, &rem.SentAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to record reminder: %w", err)
	}
	return nil
}
