package domain

import "time"

type SubscriptionStatus string

const (
	SubscriptionActive    SubscriptionStatus = "ACTIVE"
	SubscriptionExpired   SubscriptionStatus = "EXPIRED"
	SubscriptionCancelled SubscriptionStatus = "CANCELLED"
)

type Subscription struct {
	ID           int64              `json:"id"`
	SubscriberID int64              `json:"subscriber_id"`
	PlanID       int64              `json:"plan_id"`
	Status       SubscriptionStatus `json:"status"`
	StartedAt    time.Time          `json:"started_at"`
	ExpiresAt    time.Time          `json:"expires_at"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
}

func (s *Subscription) IsActive(now time.Time) bool {
	return s.Status == SubscriptionActive && s.ExpiresAt.After(now)
}

// CalculateExtension computes the new expiration date when a monthly billing is approved.
// If active and expires_at > now, extends 1 month from existing expires_at, preserving the fixed cycle day.
// If already expired (expires_at <= now), aligns to the fixed cycle day of the next month.
func (s *Subscription) CalculateExtension(now time.Time) time.Time {
	cycleDay := s.ExpiresAt.Day()
	if cycleDay <= 0 || cycleDay > 28 {
		cycleDay = 1
	}

	if s.Status == SubscriptionActive && s.ExpiresAt.After(now) {
		nextMonth := s.ExpiresAt.AddDate(0, 1, 0)
		return time.Date(nextMonth.Year(), nextMonth.Month(), cycleDay, nextMonth.Hour(), nextMonth.Minute(), nextMonth.Second(), 0, nextMonth.Location())
	}

	nextMonth := now.AddDate(0, 1, 0)
	return time.Date(nextMonth.Year(), nextMonth.Month(), cycleDay, 23, 59, 59, 0, time.UTC)
}
