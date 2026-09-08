package domain_test

import (
	"testing"
	"time"

	"wabill/internal/domain"
)

func TestInvoiceTransitions(t *testing.T) {
	tests := []struct {
		name    string
		from    domain.InvoiceStatus
		to      domain.InvoiceStatus
		wantErr bool
	}{
		{"Pending to PendingReview", domain.InvoicePending, domain.InvoicePendingReview, false},
		{"Pending to Expired", domain.InvoicePending, domain.InvoiceExpired, false},
		{"Pending to Cancelled", domain.InvoicePending, domain.InvoiceCancelled, false},
		{"Pending to Paid directly (invalid)", domain.InvoicePending, domain.InvoicePaid, true},
		{"PendingReview to Paid", domain.InvoicePendingReview, domain.InvoicePaid, false},
		{"PendingReview to Rejected", domain.InvoicePendingReview, domain.InvoiceRejected, false},
		{"Expired to Paid (invalid)", domain.InvoiceExpired, domain.InvoicePaid, true},
		{"Paid to Cancelled (invalid)", domain.InvoicePaid, domain.InvoiceCancelled, true},
		{"Rejected to Paid (invalid)", domain.InvoiceRejected, domain.InvoicePaid, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv := &domain.Invoice{Status: tt.from}
			err := inv.CanTransition(tt.to)
			if (err != nil) != tt.wantErr {
				t.Errorf("CanTransition(%s -> %s) error = %v, wantErr %v", tt.from, tt.to, err, tt.wantErr)
			}
		})
	}
}

func TestInvoiceCanAcceptProof(t *testing.T) {
	now := time.Now()

	activeInv := &domain.Invoice{
		Status:    domain.InvoicePending,
		ExpiresAt: now.Add(10 * time.Minute),
	}
	if err := activeInv.CanAcceptProof(now); err != nil {
		t.Errorf("expected active invoice to accept proof, got: %v", err)
	}

	expiredInv := &domain.Invoice{
		Status:    domain.InvoicePending,
		ExpiresAt: now.Add(-5 * time.Minute),
	}
	if err := expiredInv.CanAcceptProof(now); err != domain.ErrInvoiceExpired {
		t.Errorf("expected ErrInvoiceExpired, got: %v", err)
	}

	alreadyReviewInv := &domain.Invoice{
		Status:    domain.InvoicePendingReview,
		ExpiresAt: now.Add(10 * time.Minute),
	}
	if err := alreadyReviewInv.CanAcceptProof(now); err != domain.ErrProofAlreadySubmitted {
		t.Errorf("expected ErrProofAlreadySubmitted, got: %v", err)
	}
}
