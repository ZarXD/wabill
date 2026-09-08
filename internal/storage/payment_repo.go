package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"wabill/internal/domain"
)

type PaymentRepo struct {
	db *DB
}

func NewPaymentRepo(db *DB) *PaymentRepo {
	return &PaymentRepo{db: db}
}

func (r *PaymentRepo) SavePaymentProof(ctx context.Context, proof *domain.PaymentProof) error {
	query := `
		INSERT INTO payment_proofs (invoice_id, file_path, file_size, mime_type, uploaded_at)
		VALUES ($1, $2, $3, $4, NOW())
		RETURNING id, uploaded_at
	`
	err := r.db.QueryRowContext(ctx, query, proof.InvoiceID, proof.FilePath, proof.FileSize, proof.MimeType).
		Scan(&proof.ID, &proof.UploadedAt)
	if err != nil {
		return fmt.Errorf("failed to save payment proof: %w", err)
	}
	return nil
}

func (r *PaymentRepo) GetPaymentProofByInvoiceID(ctx context.Context, invoiceID int64) (*domain.PaymentProof, error) {
	query := `
		SELECT id, invoice_id, file_path, file_size, mime_type, uploaded_at
		FROM payment_proofs
		WHERE invoice_id = $1
		ORDER BY uploaded_at DESC
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, invoiceID)
	var p domain.PaymentProof
	err := row.Scan(&p.ID, &p.InvoiceID, &p.FilePath, &p.FileSize, &p.MimeType, &p.UploadedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get payment proof: %w", err)
	}
	return &p, nil
}

// ApproveInvoiceTx executes the atomic approval transaction:
// 1. Lock invoice with FOR UPDATE
// 2. Validate invoice is PENDING_REVIEW
// 3. Mark invoice as PAID
// 4. Create payment record
// 5. Lock subscription with FOR UPDATE
// 6. Extend subscription using domain logic (CalculateExtension)
// 7. Insert audit log
// 8. Commit
func (r *PaymentRepo) ApproveInvoiceTx(
	ctx context.Context,
	invoiceNumber string,
	adminJID string,
	now time.Time,
) (*domain.Invoice, *domain.Subscription, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Lock invoice row
	lockInvoiceQuery := `
		SELECT id, invoice_number, subscriber_id, subscription_id, plan_id, billing_period, amount,
		       status, expires_at, created_at, updated_at
		FROM invoices
		WHERE invoice_number = $1
		FOR UPDATE
	`
	var inv domain.Invoice
	row := tx.QueryRowContext(ctx, lockInvoiceQuery, invoiceNumber)
	err = row.Scan(
		&inv.ID, &inv.InvoiceNumber, &inv.SubscriberID, &inv.SubscriptionID, &inv.PlanID, &inv.BillingPeriod,
		&inv.Amount, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt, &inv.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, domain.ErrInvoiceNotFound
		}
		return nil, nil, fmt.Errorf("failed to lock invoice: %w", err)
	}

	// 2. Validate status
	if inv.Status != domain.InvoicePendingReview {
		if inv.Status == domain.InvoicePaid {
			// Idempotent: already paid, retrieve existing subscription and return without re-extending
			var sub domain.Subscription
			subRow := tx.QueryRowContext(ctx, `
				SELECT id, subscriber_id, plan_id, status, started_at, expires_at, created_at, updated_at
				FROM subscriptions WHERE id = $1`, inv.SubscriptionID)
			_ = subRow.Scan(&sub.ID, &sub.SubscriberID, &sub.PlanID, &sub.Status, &sub.StartedAt, &sub.ExpiresAt, &sub.CreatedAt, &sub.UpdatedAt)
			return &inv, &sub, nil
		}
		return nil, nil, domain.ErrInvoiceNotPendingReview
	}

	// 3. Mark invoice PAID
	updateInvoiceQuery := `
		UPDATE invoices
		SET status = 'PAID', updated_at = NOW()
		WHERE id = $1
	`
	if _, err := tx.ExecContext(ctx, updateInvoiceQuery, inv.ID); err != nil {
		return nil, nil, fmt.Errorf("failed to update invoice to PAID: %w", err)
	}
	inv.Status = domain.InvoicePaid

	// 4. Create payment record
	insertPaymentQuery := `
		INSERT INTO payments (invoice_id, amount, payment_method, paid_at, approved_by, created_at)
		VALUES ($1, $2, 'MANUAL_TRANSFER', NOW(), $3, NOW())
	`
	if _, err := tx.ExecContext(ctx, insertPaymentQuery, inv.ID, inv.Amount, adminJID); err != nil {
		return nil, nil, fmt.Errorf("failed to insert payment record: %w", err)
	}

	// 5. Lock and retrieve subscription
	lockSubQuery := `
		SELECT id, subscriber_id, plan_id, status, started_at, expires_at, created_at, updated_at
		FROM subscriptions
		WHERE id = $1
		FOR UPDATE
	`
	var sub domain.Subscription
	subRow := tx.QueryRowContext(ctx, lockSubQuery, inv.SubscriptionID)
	err = subRow.Scan(&sub.ID, &sub.SubscriberID, &sub.PlanID, &sub.Status, &sub.StartedAt, &sub.ExpiresAt, &sub.CreatedAt, &sub.UpdatedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to lock subscription: %w", err)
	}

	// 6. Extend subscription
	newExpiry := sub.CalculateExtension(now)
	updateSubQuery := `
		UPDATE subscriptions
		SET expires_at = $1, status = 'ACTIVE', updated_at = NOW()
		WHERE id = $2
	`
	if _, err := tx.ExecContext(ctx, updateSubQuery, newExpiry, sub.ID); err != nil {
		return nil, nil, fmt.Errorf("failed to update subscription expiry: %w", err)
	}
	sub.ExpiresAt = newExpiry
	sub.Status = domain.SubscriptionActive

	// 7. Audit log
	auditMeta, _ := json.Marshal(map[string]any{
		"invoice_id":     inv.ID,
		"invoice_number": inv.InvoiceNumber,
		"amount":         inv.Amount,
		"new_expires_at": newExpiry,
		"approved_by":    adminJID,
	})
	insertAuditQuery := `
		INSERT INTO audit_logs (actor, action, entity_type, entity_id, metadata, created_at)
		VALUES ($1, $2, 'INVOICE', $3, $4, NOW())
	`
	_, _ = tx.ExecContext(ctx, insertAuditQuery, adminJID, domain.AuditActionInvoiceApproved, fmt.Sprintf("%d", inv.ID), string(auditMeta))

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("failed to commit approval transaction: %w", err)
	}

	return &inv, &sub, nil
}

// RejectInvoiceTx executes the atomic rejection transaction:
func (r *PaymentRepo) RejectInvoiceTx(
	ctx context.Context,
	invoiceNumber string,
	adminJID string,
	reason string,
	now time.Time,
) (*domain.Invoice, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Lock invoice row
	lockInvoiceQuery := `
		SELECT id, invoice_number, subscriber_id, subscription_id, plan_id, billing_period, amount,
		       status, expires_at, created_at, updated_at
		FROM invoices
		WHERE invoice_number = $1
		FOR UPDATE
	`
	var inv domain.Invoice
	row := tx.QueryRowContext(ctx, lockInvoiceQuery, invoiceNumber)
	err = row.Scan(
		&inv.ID, &inv.InvoiceNumber, &inv.SubscriberID, &inv.SubscriptionID, &inv.PlanID, &inv.BillingPeriod,
		&inv.Amount, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt, &inv.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrInvoiceNotFound
		}
		return nil, fmt.Errorf("failed to lock invoice: %w", err)
	}

	if inv.Status != domain.InvoicePendingReview {
		return nil, domain.ErrInvoiceNotPendingReview
	}

	// 2. Mark invoice REJECTED
	updateInvoiceQuery := `
		UPDATE invoices
		SET status = 'REJECTED', rejected_at = NOW(), rejected_by = $1, rejection_reason = $2, updated_at = NOW()
		WHERE id = $3
	`
	if _, err := tx.ExecContext(ctx, updateInvoiceQuery, adminJID, reason, inv.ID); err != nil {
		return nil, fmt.Errorf("failed to update invoice to REJECTED: %w", err)
	}
	inv.Status = domain.InvoiceRejected
	inv.RejectedAt = &now
	inv.RejectedBy = &adminJID
	inv.RejectionReason = &reason

	// 3. Audit log
	auditMeta, _ := json.Marshal(map[string]any{
		"invoice_id":     inv.ID,
		"invoice_number": inv.InvoiceNumber,
		"rejected_by":    adminJID,
		"reason":         reason,
	})
	insertAuditQuery := `
		INSERT INTO audit_logs (actor, action, entity_type, entity_id, metadata, created_at)
		VALUES ($1, $2, 'INVOICE', $3, $4, NOW())
	`
	_, _ = tx.ExecContext(ctx, insertAuditQuery, adminJID, domain.AuditActionInvoiceRejected, fmt.Sprintf("%d", inv.ID), string(auditMeta))

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit rejection transaction: %w", err)
	}

	return &inv, nil
}
