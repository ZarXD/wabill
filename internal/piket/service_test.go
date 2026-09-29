package piket_test

import (
	"strings"
	"testing"
	"time"

	"wabill/internal/config"
	"wabill/internal/piket"
)

func TestPiketWeeklyScheduleRotation(t *testing.T) {
	loc := time.FixedZone("WIB", 7*3600)
	numSlots := 3

	// Tuesday 2026-09-29 is refDate (Slot #1, index 0)
	tue29 := time.Date(2026, 9, 29, 12, 0, 0, 0, loc)
	sun, idx := piket.CalculatePiketSlot(tue29, loc, numSlots)
	if sun || idx != 0 {
		t.Fatalf("expected Tue 29 Sep to be slot 0, got sun=%v, idx=%d", sun, idx)
	}

	// Wednesday 2026-09-30 (Slot #2, index 1)
	wed30 := tue29.AddDate(0, 0, 1)
	sun, idx = piket.CalculatePiketSlot(wed30, loc, numSlots)
	if sun || idx != 1 {
		t.Fatalf("expected Wed 30 Sep to be slot 1, got sun=%v, idx=%d", sun, idx)
	}

	// Thursday 2026-10-01 (Slot #3, index 2)
	thu01 := tue29.AddDate(0, 0, 2)
	sun, idx = piket.CalculatePiketSlot(thu01, loc, numSlots)
	if sun || idx != 2 {
		t.Fatalf("expected Thu 01 Oct to be slot 2, got sun=%v, idx=%d", sun, idx)
	}

	// Friday 2026-10-02 (Slot #1, index 0)
	fri02 := tue29.AddDate(0, 0, 3)
	sun, idx = piket.CalculatePiketSlot(fri02, loc, numSlots)
	if sun || idx != 0 {
		t.Fatalf("expected Fri 02 Oct to be slot 0, got sun=%v, idx=%d", sun, idx)
	}

	// Saturday 2026-10-03 (Slot #2, index 1)
	sat03 := tue29.AddDate(0, 0, 4)
	sun, idx = piket.CalculatePiketSlot(sat03, loc, numSlots)
	if sun || idx != 1 {
		t.Fatalf("expected Sat 03 Oct to be slot 1, got sun=%v, idx=%d", sun, idx)
	}

	// Sunday 2026-10-04 (Piket Bersama: isSunday = true)
	sun04 := tue29.AddDate(0, 0, 5)
	sun, idx = piket.CalculatePiketSlot(sun04, loc, numSlots)
	if !sun {
		t.Fatalf("expected Sun 04 Oct to have isSunday=true, got sun=%v, idx=%d", sun, idx)
	}

	// Monday 2026-10-05 (Slot #3, index 2 - resumes seamlessly after Saturday's Slot #2!)
	mon05 := tue29.AddDate(0, 0, 6)
	sun, idx = piket.CalculatePiketSlot(mon05, loc, numSlots)
	if sun || idx != 2 {
		t.Fatalf("expected Mon 05 Oct to be slot 2 (resuming after Saturday), got sun=%v, idx=%d", sun, idx)
	}

	// Tuesday 2026-10-06 (Slot #1, index 0)
	tue06 := tue29.AddDate(0, 0, 7)
	sun, idx = piket.CalculatePiketSlot(tue06, loc, numSlots)
	if sun || idx != 0 {
		t.Fatalf("expected Tue 06 Oct to be slot 0, got sun=%v, idx=%d", sun, idx)
	}
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
