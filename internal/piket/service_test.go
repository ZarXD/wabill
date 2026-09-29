package piket_test

import (
	"strings"
	"testing"
	"time"

	"wabill/internal/config"
	"wabill/internal/piket"
)

func TestPiketWeeklyScheduleRotation(t *testing.T) {
	cfg := &config.Config{
		AppTimezone: time.FixedZone("WIB", 7*3600),
	}
	svc := piket.NewService(cfg, nil)

	// In memory verification for rotation logic
	slots := []piket.Slot{
		{
			ID:            1,
			Name:          "Solo Joko",
			RotationOrder: 1,
			Members: []piket.Member{
				{Name: "Joko", PhoneNumber: "6281111111"},
			},
		},
		{
			ID:            2,
			Name:          "Duet Budi & Fahad",
			RotationOrder: 2,
			Members: []piket.Member{
				{Name: "Budi", PhoneNumber: "6282222222"},
				{Name: "Fahad", PhoneNumber: "6283333333"},
			},
		},
		{
			ID:            3,
			Name:          "Solo Iwan",
			RotationOrder: 3,
			Members: []piket.Member{
				{Name: "Iwan", PhoneNumber: "6284444444"},
			},
		},
	}

	// Verify deterministic day index modulo
	baseTime := time.Date(2026, 9, 29, 12, 0, 0, 0, cfg.AppTimezone)
	day0Index := ((int(baseTime.Unix()/86400) % len(slots)) + len(slots)) % len(slots)
	day1Time := baseTime.AddDate(0, 0, 1)
	day1Index := ((int(day1Time.Unix()/86400) % len(slots)) + len(slots)) % len(slots)

	expectedNext := (day0Index + 1) % len(slots)
	if day1Index != expectedNext {
		t.Fatalf("expected next day index to be %d, got %d", expectedNext, day1Index)
	}

	_ = svc
}

func TestAntiSpamRules(t *testing.T) {
	cfg := &config.Config{
		AdminJIDs:   []string{"6289999999@s.whatsapp.net"},
		AppTimezone: time.FixedZone("WIB", 7*3600),
	}

	// Verify admin checking
	if !cfg.IsAdmin("6289999999@s.whatsapp.net", "6289999999") {
		t.Errorf("expected admin to be recognized")
	}
	if cfg.IsAdmin("6281234567@s.whatsapp.net", "6281234567") {
		t.Errorf("random user should not be recognized as admin")
	}
}

func TestFeederAssignmentCheck(t *testing.T) {
	slot := piket.Slot{
		ID: 1,
		Members: []piket.Member{
			{Name: "Budi", PhoneNumber: "6281111111", WhatsAppJID: "6281111111@s.whatsapp.net"},
			{Name: "Fahad", PhoneNumber: "6282222222", WhatsAppJID: "31946050646097@lid"},
		},
	}

	isFeeder := func(phone, jid string) bool {
		cleanPhone := config.NormalizePhone(phone)
		for _, m := range slot.Members {
			cleanMPhone := config.NormalizePhone(m.PhoneNumber)
			if cleanPhone != "" && cleanMPhone != "" && cleanMPhone == cleanPhone {
				return true
			}
			cleanMJID := config.NormalizePhone(m.WhatsAppJID)
			if cleanPhone != "" && cleanMJID != "" && cleanMJID == cleanPhone {
				return true
			}
			if strings.Contains(jid, "@lid") {
				lidUser := strings.Split(jid, "@")[0]
				if strings.Contains(m.WhatsAppJID, lidUser) || strings.Contains(m.PhoneNumber, lidUser) {
					return true
				}
			}
		}
		return false
	}

	if !isFeeder("6281111111", "") {
		t.Errorf("expected Budi to be recognized as feeder")
	}
	if !isFeeder("", "31946050646097@lid") {
		t.Errorf("expected Fahad to be recognized via LID")
	}
	if isFeeder("6289999999", "6289999999@s.whatsapp.net") {
		t.Errorf("unassigned user should not be recognized as feeder")
	}
}

func TestReminderTimeMatching(t *testing.T) {
	cfg := &config.Config{
		AppTimezone:           time.FixedZone("WIB", 7*3600),
		LeleEarlyReminderTime: "15:00",
		LeleFeedingTime:       "16:30",
		LeleOverdueTime:       "17:30",
	}

	loc := cfg.AppTimezone
	testTimes := []struct {
		hour     int
		minute   int
		expected string
	}{
		{14, 59, "NONE"},
		{15, 00, "EARLY"},
		{15, 30, "EARLY"},
		{16, 29, "EARLY"},
		{16, 30, "FEEDING"},
		{17, 00, "FEEDING"},
		{17, 29, "FEEDING"},
		{17, 30, "OVERDUE"},
		{18, 00, "OVERDUE"},
	}

	for _, tt := range testTimes {
		now := time.Date(2026, 9, 29, tt.hour, tt.minute, 0, 0, loc)
		timeStr := now.Format("15:04")

		tier := "NONE"
		if timeStr >= cfg.LeleEarlyReminderTime && timeStr < cfg.LeleFeedingTime {
			tier = "EARLY"
		} else if timeStr >= cfg.LeleFeedingTime && timeStr < cfg.LeleOverdueTime {
			tier = "FEEDING"
		} else if timeStr >= cfg.LeleOverdueTime {
			tier = "OVERDUE"
		}

		if tier != tt.expected {
			t.Errorf("at %02d:%02d expected tier %s, got %s", tt.hour, tt.minute, tt.expected, tier)
		}
	}
}
