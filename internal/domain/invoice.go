package domain

import (
	"fmt"
	"time"
)

type InvoiceStatus string

const (
	InvoicePending       InvoiceStatus = "PENDING"
	InvoicePendingReview InvoiceStatus = "PENDING_REVIEW"
	InvoicePaid          InvoiceStatus = "PAID"
	InvoiceExpired       InvoiceStatus = "EXPIRED"
	InvoiceCancelled     InvoiceStatus = "CANCELLED"
	InvoiceRejected      InvoiceStatus = "REJECTED"
)

type Invoice struct {
	ID              int64         `json:"id"`
	InvoiceNumber   string        `json:"invoice_number"`
	SubscriberID    int64         `json:"subscriber_id"`
	SubscriptionID  int64         `json:"subscription_id"`
	PlanID          int64         `json:"plan_id"`
	BillingPeriod   string        `json:"billing_period"` // e.g. "2026-09"
	Amount          int64         `json:"amount"`
	Status          InvoiceStatus `json:"status"`
	ExpiresAt       time.Time     `json:"expires_at"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
	RejectedAt      *time.Time    `json:"rejected_at,omitempty"`
	RejectedBy      *string       `json:"rejected_by,omitempty"`
	RejectionReason *string       `json:"rejection_reason,omitempty"`
}

func (i *Invoice) IsActive(now time.Time) bool {
	return i.Status == InvoicePending && i.ExpiresAt.After(now)
}

func (i *Invoice) CanAcceptProof(now time.Time) error {
	if i.Status != InvoicePending {
		if i.Status == InvoicePendingReview {
			return ErrProofAlreadySubmitted
		}
		if i.Status == InvoiceExpired || i.ExpiresAt.Before(now) {
			return ErrInvoiceExpired
		}
		return ErrInvoiceNotPending
	}
	if i.ExpiresAt.Before(now) {
		return ErrInvoiceExpired
	}
	return nil
}

// CanTransition validates whether transitioning from current status to next status is permitted.
func (i *Invoice) CanTransition(to InvoiceStatus) error {
	valid := false
	switch i.Status {
	case InvoicePending:
		valid = (to == InvoicePendingReview || to == InvoiceExpired || to == InvoiceCancelled)
	case InvoicePendingReview:
		valid = (to == InvoicePaid || to == InvoiceRejected)
	default:
		// Final states (PAID, EXPIRED, CANCELLED, REJECTED) cannot transition
		valid = false
	}

	if !valid {
		return fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidStateTransition, i.Status, to)
	}
	return nil
}

func (i *Invoice) IsFinal() bool {
	return i.Status == InvoicePaid || i.Status == InvoiceExpired || i.Status == InvoiceCancelled || i.Status == InvoiceRejected
}
