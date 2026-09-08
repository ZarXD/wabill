package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"wabill/internal/domain"
)

type SubscriberRepo struct {
	db *DB
}

func NewSubscriberRepo(db *DB) *SubscriberRepo {
	return &SubscriberRepo{db: db}
}

func (r *SubscriberRepo) GetSubscriberByJID(ctx context.Context, jid string) (*domain.Subscriber, error) {
	query := `
		SELECT id, name, phone_number, whatsapp_jid, status, created_at, updated_at
		FROM subscribers
		WHERE whatsapp_jid = $1
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, jid)
	var s domain.Subscriber
	err := row.Scan(&s.ID, &s.Name, &s.PhoneNumber, &s.WhatsAppJID, &s.Status, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrSubscriberNotFound
		}
		return nil, fmt.Errorf("failed to get subscriber by jid: %w", err)
	}
	return &s, nil
}

func (r *SubscriberRepo) GetSubscriberByID(ctx context.Context, id int64) (*domain.Subscriber, error) {
	query := `
		SELECT id, name, phone_number, whatsapp_jid, status, created_at, updated_at
		FROM subscribers
		WHERE id = $1
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, id)
	var s domain.Subscriber
	err := row.Scan(&s.ID, &s.Name, &s.PhoneNumber, &s.WhatsAppJID, &s.Status, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrSubscriberNotFound
		}
		return nil, fmt.Errorf("failed to get subscriber by id: %w", err)
	}
	return &s, nil
}

func (r *SubscriberRepo) CreateSubscriber(ctx context.Context, s *domain.Subscriber) error {
	query := `
		INSERT INTO subscribers (name, phone_number, whatsapp_jid, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		RETURNING id, created_at, updated_at
	`
	err := r.db.QueryRowContext(ctx, query, s.Name, s.PhoneNumber, s.WhatsAppJID, s.Status).
		Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create subscriber: %w", err)
	}
	return nil
}

func (r *SubscriberRepo) GetPlanByID(ctx context.Context, id int64) (*domain.Plan, error) {
	query := `
		SELECT id, name, description, price, billing_interval, active, created_at, updated_at
		FROM plans
		WHERE id = $1
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, id)
	var p domain.Plan
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Price, &p.BillingInterval, &p.Active, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrPlanNotFound
		}
		return nil, fmt.Errorf("failed to get plan by id: %w", err)
	}
	return &p, nil
}

func (r *SubscriberRepo) GetDefaultPlan(ctx context.Context) (*domain.Plan, error) {
	query := `
		SELECT id, name, description, price, billing_interval, active, created_at, updated_at
		FROM plans
		WHERE active = true
		ORDER BY id ASC
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query)
	var p domain.Plan
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Price, &p.BillingInterval, &p.Active, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrPlanNotFound
		}
		return nil, fmt.Errorf("failed to get default plan: %w", err)
	}
	return &p, nil
}

func (r *SubscriberRepo) CreatePlan(ctx context.Context, p *domain.Plan) error {
	query := `
		INSERT INTO plans (name, description, price, billing_interval, active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		RETURNING id, created_at, updated_at
	`
	err := r.db.QueryRowContext(ctx, query, p.Name, p.Description, p.Price, p.BillingInterval, p.Active).
		Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create plan: %w", err)
	}
	return nil
}

func (r *SubscriberRepo) GetSubscriberByPhoneOrJID(ctx context.Context, phone, jid string) (*domain.Subscriber, error) {
	query := `
		SELECT id, name, phone_number, whatsapp_jid, status, created_at, updated_at
		FROM subscribers
		WHERE (phone_number = $1 AND $1 <> '') OR (whatsapp_jid = $2 AND $2 <> '')
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, phone, jid)
	var s domain.Subscriber
	err := row.Scan(&s.ID, &s.Name, &s.PhoneNumber, &s.WhatsAppJID, &s.Status, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrSubscriberNotFound
		}
		return nil, fmt.Errorf("failed to get subscriber by phone/jid: %w", err)
	}
	return &s, nil
}

func (r *SubscriberRepo) GetAllSubscribers(ctx context.Context) ([]*domain.Subscriber, error) {
	query := `
		SELECT id, name, phone_number, whatsapp_jid, status, created_at, updated_at
		FROM subscribers
		ORDER BY id ASC
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query all subscribers: %w", err)
	}
	defer rows.Close()

	var list []*domain.Subscriber
	for rows.Next() {
		var s domain.Subscriber
		if err := rows.Scan(&s.ID, &s.Name, &s.PhoneNumber, &s.WhatsAppJID, &s.Status, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan subscriber: %w", err)
		}
		list = append(list, &s)
	}
	return list, rows.Err()
}

func (r *SubscriberRepo) UpdateSubscriberStatus(ctx context.Context, id int64, status domain.SubscriberStatus) error {
	query := `
		UPDATE subscribers
		SET status = $1, updated_at = NOW()
		WHERE id = $2
	`
	_, err := r.db.ExecContext(ctx, query, status, id)
	if err != nil {
		return fmt.Errorf("failed to update subscriber status: %w", err)
	}
	return nil
}

func (r *SubscriberRepo) GetPlanByName(ctx context.Context, name string) (*domain.Plan, error) {
	query := `
		SELECT id, name, description, price, billing_interval, active, created_at, updated_at
		FROM plans
		WHERE LOWER(name) LIKE LOWER($1)
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, "%"+name+"%")
	var p domain.Plan
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.Price, &p.BillingInterval, &p.Active, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrPlanNotFound
		}
		return nil, fmt.Errorf("failed to get plan by name: %w", err)
	}
	return &p, nil
}
