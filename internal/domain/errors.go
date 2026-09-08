package domain

import "errors"

var (
	ErrSubscriberNotFound       = errors.New("subscriber not found")
	ErrActiveInvoiceExists      = errors.New("an active unpaid invoice already exists")
	ErrInvoiceNotFound          = errors.New("invoice not found")
	ErrInvoiceExpired           = errors.New("invoice is expired")
	ErrInvoiceNotPending        = errors.New("invoice is not in pending status")
	ErrInvoiceNotPendingReview  = errors.New("invoice is not in pending review status")
	ErrInvalidStateTransition   = errors.New("invalid invoice state transition")
	ErrUnauthorizedAdmin        = errors.New("sender is not an authorized admin")
	ErrNoActiveSubscription     = errors.New("no active subscription found")
	ErrPlanNotFound             = errors.New("subscription plan not found")
	ErrInvalidProofMedia        = errors.New("unsupported or invalid media format for payment proof")
	ErrFileTooLarge             = errors.New("payment proof file size exceeds maximum allowed limit")
	ErrProofAlreadySubmitted    = errors.New("proof has already been submitted for this invoice")
	ErrDuplicateEvent           = errors.New("event has already been processed")
)
