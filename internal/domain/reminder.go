package domain

import "time"

type Reminder struct {
	ID             int64     `json:"id"`
	SubscriptionID int64     `json:"subscription_id"`
	BillingPeriod  string    `json:"billing_period"`
	ReminderType   string    `json:"reminder_type"`
	SentAt         time.Time `json:"sent_at"`
}
