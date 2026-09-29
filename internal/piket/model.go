package piket

import (
	"time"
)

type Status string

const (
	StatusPending Status = "PENDING"
	StatusDone    Status = "DONE"
)

// Member represents an individual team member assigned to a piket slot.
type Member struct {
	ID          int64     `json:"id"`
	SlotID      int64     `json:"slot_id"`
	Name        string    `json:"name"`
	PhoneNumber string    `json:"phone_number"`
	WhatsAppJID string    `json:"whatsapp_jid"`
	CreatedAt   time.Time `json:"created_at"`
}

// Slot represents a feeding duty slot in the rotation (Solo: 1 member, Duet: 2 members).
type Slot struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	RotationOrder int       `json:"rotation_order"`
	Active        bool      `json:"active"`
	Members       []Member  `json:"members"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Log represents the daily feeding duty record and proof.
type Log struct {
	ID                     int64      `json:"id"`
	FeedingDate            string     `json:"feeding_date"` // YYYY-MM-DD
	SlotID                 *int64     `json:"slot_id"`
	AssignedMembersDisplay string     `json:"assigned_members_display"`
	Status                 Status     `json:"status"`
	FedAt                  *time.Time `json:"fed_at"`
	ProofFilePath          string     `json:"proof_file_path"`
	ConfirmedBy            string     `json:"confirmed_by"`
	EarlyRemindedAt        *time.Time `json:"early_reminded_at"`
	FeedingRemindedAt      *time.Time `json:"feeding_reminded_at"`
	OverdueRemindedAt      *time.Time `json:"overdue_reminded_at"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}
