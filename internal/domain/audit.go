package domain

import "time"

const (
	AuditActionInvoiceCreated       = "INVOICE_CREATED"
	AuditActionProofSubmitted       = "PROOF_SUBMITTED"
	AuditActionInvoiceApproved      = "INVOICE_APPROVED"
	AuditActionInvoiceRejected      = "INVOICE_REJECTED"
	AuditActionSubscriptionExtended = "SUBSCRIPTION_EXTENDED"
	AuditActionReminderSent         = "REMINDER_SENT"
	AuditActionInvoiceExpired       = "INVOICE_EXPIRED"
)

type AuditLog struct {
	ID         int64     `json:"id"`
	Actor      string    `json:"actor"`
	Action     string    `json:"action"`
	EntityType string    `json:"entity_type"`
	EntityID   string    `json:"entity_id"`
	Metadata   string    `json:"metadata"`
	CreatedAt  time.Time `json:"created_at"`
}
