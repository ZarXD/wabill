package domain_test

import (
	"testing"
	"time"

	"wabill/internal/domain"
)

func TestSubscriptionCalculateExtension(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	// Case 1: Subscription is active and expires in future (e.g. 2026-09-15)
	existingExpiry := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	subActive := &domain.Subscription{
		Status:    domain.SubscriptionActive,
		ExpiresAt: existingExpiry,
	}

	extendedActive := subActive.CalculateExtension(now)
	expectedActive := existingExpiry.AddDate(0, 1, 0) // should be 2026-10-15
	if !extendedActive.Equal(expectedActive) {
		t.Errorf("expected active extension to be %v, got %v", expectedActive, extendedActive)
	}

	// Case 2: Subscription is already expired with cycle day 1 (e.g. expired on 2026-09-01)
	pastExpiry := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	subExpired := &domain.Subscription{
		Status:    domain.SubscriptionExpired,
		ExpiresAt: pastExpiry,
	}

	extendedExpired := subExpired.CalculateExtension(now)
	// Aligns to cycleDay (day 1) of the next month (2026-10-01 23:59:59 UTC)
	expectedExpired := time.Date(2026, 10, 1, 23, 59, 59, 0, time.UTC)
	if !extendedExpired.Equal(expectedExpired) {
		t.Errorf("expected expired extension to be %v, got %v", expectedExpired, extendedExpired)
	}
}
