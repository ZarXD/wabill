package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"wabill/internal/domain"
)

type InvoiceRepo struct {
	db *DB
}

func NewInvoiceRepo(db *DB) *InvoiceRepo {
	return &InvoiceRepo{db: db}
}

func (r *InvoiceRepo) GetActiveInvoiceBySubscriberID(ctx context.Context, subscriberID int64) (*domain.Invoice, error) {
	query := `
		SELECT id, invoice_number, subscriber_id, subscription_id, plan_id, billing_period, amount,
		       status, expires_at, created_at, updated_at, rejected_at, rejected_by, rejection_reason
		FROM invoices
		WHERE subscriber_id = $1
		  AND status = 'PENDING'
		  AND expires_at > NOW()
		ORDER BY expires_at DESC
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, subscriberID)
	return scanInvoice(row)
}

func (r *InvoiceRepo) GetLatestInvoiceBySubscriberID(ctx context.Context, subscriberID int64) (*domain.Invoice, error) {
	query := `
		SELECT id, invoice_number, subscriber_id, subscription_id, plan_id, billing_period, amount,
		       status, expires_at, created_at, updated_at, rejected_at, rejected_by, rejection_reason
		FROM invoices
		WHERE subscriber_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, subscriberID)
	return scanInvoice(row)
}

func (r *InvoiceRepo) GetInvoiceByNumber(ctx context.Context, number string) (*domain.Invoice, error) {
	query := `
		SELECT id, invoice_number, subscriber_id, subscription_id, plan_id, billing_period, amount,
		       status, expires_at, created_at, updated_at, rejected_at, rejected_by, rejection_reason
		FROM invoices
		WHERE invoice_number = $1
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, number)
	return scanInvoice(row)
}

func (r *InvoiceRepo) GetInvoiceByID(ctx context.Context, id int64) (*domain.Invoice, error) {
	query := `
		SELECT id, invoice_number, subscriber_id, subscription_id, plan_id, billing_period, amount,
		       status, expires_at, created_at, updated_at, rejected_at, rejected_by, rejection_reason
		FROM invoices
		WHERE id = $1
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, id)
	return scanInvoice(row)
}

func (r *InvoiceRepo) GetPendingReviewInvoices(ctx context.Context) ([]*domain.Invoice, error) {
	query := `
		SELECT id, invoice_number, subscriber_id, subscription_id, plan_id, billing_period, amount,
		       status, expires_at, created_at, updated_at, rejected_at, rejected_by, rejection_reason
		FROM invoices
		WHERE status = 'PENDING_REVIEW'
		ORDER BY created_at ASC
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending review invoices: %w", err)
	}
	defer rows.Close()

	var invoices []*domain.Invoice
	for rows.Next() {
		var inv domain.Invoice
		var rejAt sql.NullTime
		var rejBy, rejReason sql.NullString
		err := rows.Scan(
			&inv.ID, &inv.InvoiceNumber, &inv.SubscriberID, &inv.SubscriptionID, &inv.PlanID, &inv.BillingPeriod,
			&inv.Amount, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt, &inv.UpdatedAt,
			&rejAt, &rejBy, &rejReason,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan invoice: %w", err)
		}
		if rejAt.Valid {
			inv.RejectedAt = &rejAt.Time
		}
		if rejBy.Valid {
			inv.RejectedBy = &rejBy.String
		}
		if rejReason.Valid {
			inv.RejectionReason = &rejReason.String
		}
		invoices = append(invoices, &inv)
	}
	return invoices, rows.Err()
}

func (r *InvoiceRepo) GetRecentInvoicesBySubscriberID(ctx context.Context, subscriberID int64, limit int) ([]*domain.Invoice, error) {
	query := `
		SELECT id, invoice_number, subscriber_id, subscription_id, plan_id, billing_period, amount,
		       status, expires_at, created_at, updated_at, rejected_at, rejected_by, rejection_reason
		FROM invoices
		WHERE subscriber_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`
	rows, err := r.db.QueryContext(ctx, query, subscriberID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query recent invoices: %w", err)
	}
	defer rows.Close()

	var invoices []*domain.Invoice
	for rows.Next() {
		var inv domain.Invoice
		var rejAt sql.NullTime
		var rejBy, rejReason sql.NullString
		err := rows.Scan(
			&inv.ID, &inv.InvoiceNumber, &inv.SubscriberID, &inv.SubscriptionID, &inv.PlanID, &inv.BillingPeriod,
			&inv.Amount, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt, &inv.UpdatedAt,
			&rejAt, &rejBy, &rejReason,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan invoice: %w", err)
		}
		if rejAt.Valid {
			inv.RejectedAt = &rejAt.Time
		}
		if rejBy.Valid {
			inv.RejectedBy = &rejBy.String
		}
		if rejReason.Valid {
			inv.RejectionReason = &rejReason.String
		}
		invoices = append(invoices, &inv)
	}
	return invoices, rows.Err()
}

func (r *InvoiceRepo) CreateInvoice(ctx context.Context, inv *domain.Invoice) error {
	query := `
		INSERT INTO invoices (invoice_number, subscriber_id, subscription_id, plan_id, billing_period, amount, status, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		RETURNING id, created_at, updated_at
	`
	err := r.db.QueryRowContext(ctx, query,
		inv.InvoiceNumber, inv.SubscriberID, inv.SubscriptionID, inv.PlanID, inv.BillingPeriod,
		inv.Amount, inv.Status, inv.ExpiresAt).
		Scan(&inv.ID, &inv.CreatedAt, &inv.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create invoice: %w", err)
	}
	return nil
}

func (r *InvoiceRepo) UpdateStatus(ctx context.Context, invoiceID int64, newStatus domain.InvoiceStatus) error {
	query := `
		UPDATE invoices
		SET status = $1, updated_at = NOW()
		WHERE id = $2
	`
	_, err := r.db.ExecContext(ctx, query, newStatus, invoiceID)
	if err != nil {
		return fmt.Errorf("failed to update invoice status: %w", err)
	}
	return nil
}

func (r *InvoiceRepo) MarkInvoicesExpired(ctx context.Context, now time.Time) (int64, error) {
	query := `
		UPDATE invoices
		SET status = 'EXPIRED', updated_at = NOW()
		WHERE status = 'PENDING' AND expires_at <= $1
	`
	res, err := r.db.ExecContext(ctx, query, now)
	if err != nil {
		return 0, fmt.Errorf("failed to mark invoices expired: %w", err)
	}
	return res.RowsAffected()
}

func scanInvoice(row *sql.Row) (*domain.Invoice, error) {
	var inv domain.Invoice
	var rejAt sql.NullTime
	var rejBy, rejReason sql.NullString
	err := row.Scan(
		&inv.ID, &inv.InvoiceNumber, &inv.SubscriberID, &inv.SubscriptionID, &inv.PlanID, &inv.BillingPeriod,
		&inv.Amount, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt, &inv.UpdatedAt,
		&rejAt, &rejBy, &rejReason,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrInvoiceNotFound
		}
		return nil, fmt.Errorf("failed to scan invoice: %w", err)
	}
	if rejAt.Valid {
		inv.RejectedAt = &rejAt.Time
	}
	if rejBy.Valid {
		inv.RejectedBy = &rejBy.String
	}
	if rejReason.Valid {
		inv.RejectionReason = &rejReason.String
	}
	return &inv, nil
}
