package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"wabill/internal/domain"
)

type SubscriptionRepo struct {
	db *DB
}

func NewSubscriptionRepo(db *DB) *SubscriptionRepo {
	return &SubscriptionRepo{db: db}
}

func (r *SubscriptionRepo) GetActiveSubscriptionBySubscriberID(ctx context.Context, subscriberID int64) (*domain.Subscription, error) {
	query := `
		SELECT id, subscriber_id, plan_id, status, started_at, expires_at, created_at, updated_at
		FROM subscriptions
		WHERE subscriber_id = $1 AND status = 'ACTIVE'
		ORDER BY expires_at DESC
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, subscriberID)
	var s domain.Subscription
	err := row.Scan(&s.ID, &s.SubscriberID, &s.PlanID, &s.Status, &s.StartedAt, &s.ExpiresAt, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNoActiveSubscription
		}
		return nil, fmt.Errorf("failed to get active subscription: %w", err)
	}
	return &s, nil
}

func (r *SubscriptionRepo) GetSubscriptionsApproachingExpiry(ctx context.Context, daysAhead int) ([]*domain.Subscription, error) {
	query := `
		SELECT id, subscriber_id, plan_id, status, started_at, expires_at, created_at, updated_at
		FROM subscriptions
		WHERE status = 'ACTIVE'
		  AND expires_at > NOW()
		  AND expires_at <= NOW() + ($1 || ' days')::INTERVAL
		ORDER BY expires_at ASC
	`
	rows, err := r.db.QueryContext(ctx, query, daysAhead)
	if err != nil {
		return nil, fmt.Errorf("failed to query approaching subscriptions: %w", err)
	}
	defer rows.Close()

	var subs []*domain.Subscription
	for rows.Next() {
		var s domain.Subscription
		if err := rows.Scan(&s.ID, &s.SubscriberID, &s.PlanID, &s.Status, &s.StartedAt, &s.ExpiresAt, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan subscription: %w", err)
		}
		subs = append(subs, &s)
	}
	return subs, rows.Err()
}

func (r *SubscriptionRepo) CreateSubscription(ctx context.Context, s *domain.Subscription) error {
	query := `
		INSERT INTO subscriptions (subscriber_id, plan_id, status, started_at, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		RETURNING id, created_at, updated_at
	`
	err := r.db.QueryRowContext(ctx, query, s.SubscriberID, s.PlanID, s.Status, s.StartedAt, s.ExpiresAt).
		Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create subscription: %w", err)
	}
	return nil
}

func (r *SubscriptionRepo) UpdateSubscriptionExpiry(ctx context.Context, id int64, newExpiry time.Time, status domain.SubscriptionStatus) error {
	query := `
		UPDATE subscriptions
		SET expires_at = $1, status = $2, updated_at = NOW()
		WHERE id = $3
	`
	_, err := r.db.ExecContext(ctx, query, newExpiry, status, id)
	if err != nil {
		return fmt.Errorf("failed to update subscription expiry: %w", err)
	}
	return nil
}

func (r *SubscriptionRepo) GetLatestSubscriptionBySubscriberID(ctx context.Context, subscriberID int64) (*domain.Subscription, error) {
	query := `
		SELECT id, subscriber_id, plan_id, status, started_at, expires_at, created_at, updated_at
		FROM subscriptions
		WHERE subscriber_id = $1
		ORDER BY expires_at DESC
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, subscriberID)
	var s domain.Subscription
	err := row.Scan(&s.ID, &s.SubscriberID, &s.PlanID, &s.Status, &s.StartedAt, &s.ExpiresAt, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNoActiveSubscription
		}
		return nil, fmt.Errorf("failed to get latest subscription: %w", err)
	}
	return &s, nil
}
