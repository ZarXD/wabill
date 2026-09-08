package domain

import "time"

type SubscriberStatus string

const (
	SubscriberActive    SubscriberStatus = "ACTIVE"
	SubscriberInactive  SubscriberStatus = "INACTIVE"
	SubscriberSuspended SubscriberStatus = "SUSPENDED"
)

type Subscriber struct {
	ID          int64            `json:"id"`
	Name        string           `json:"name"`
	PhoneNumber string           `json:"phone_number"`
	WhatsAppJID string           `json:"whatsapp_jid"`
	Status      SubscriberStatus `json:"status"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

func (s *Subscriber) IsActive() bool {
	return s.Status == SubscriberActive
}
