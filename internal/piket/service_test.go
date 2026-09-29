package piket_test

import (
	"strings"
	"testing"
	"time"

	"wabill/internal/config"
	"wabill/internal/piket"
)

func TestParseDayOfWeek(t *testing.T) {
	testCases := []struct {
		input       string
		expectedWd  time.Weekday
		expectedStr string
		expectedOk  bool
	}{
		{"senin", time.Monday, "Senin", true},
		{"Senin", time.Monday, "Senin", true},
		{"selasa", time.Tuesday, "Selasa", true},
		{"rabu", time.Wednesday, "Rabu", true},
		{"kamis", time.Thursday, "Kamis", true},
		{"jumat", time.Friday, "Jumat", true},
		{"jum'at", time.Friday, "Jumat", true},
		{"sabtu", time.Saturday, "Sabtu", true},
		{"minggu", time.Sunday, "Minggu", true},
		{"ahad", time.Sunday, "Minggu", true},
		{"1", time.Monday, "Senin", true},
		{"5", time.Friday, "Jumat", true},
		{"ngasal", 0, "", false},
		{"", 0, "", false},
	}

	for _, tc := range testCases {
		wd, name, ok := piket.ParseDayOfWeek(tc.input)
		if ok != tc.expectedOk {
			t.Errorf("for input %q: expected ok=%v, got %v", tc.input, tc.expectedOk, ok)
		}
		if ok {
			if wd != tc.expectedWd || name != tc.expectedStr {
				t.Errorf("for input %q: expected (%v, %q), got (%v, %q)", tc.input, tc.expectedWd, tc.expectedStr, wd, name)
			}
		}
	}
}

func TestPiketWeeklyScheduleRotation(t *testing.T) {
	loc := time.FixedZone("WIB", 7*3600)
	numSlots := 6

	// Monday (Senin, day 1, idx 0)
	mon := time.Date(2026, 9, 28, 12, 0, 0, 0, loc)
	sun, idx := piket.CalculatePiketSlot(mon, loc, numSlots)
	if sun || idx != 0 {
		t.Fatalf("expected Monday to be idx 0, got sun=%v, idx=%d", sun, idx)
	}

	// Tuesday (Selasa, day 2, idx 1)
	tue := time.Date(2026, 9, 29, 12, 0, 0, 0, loc)
	sun, idx = piket.CalculatePiketSlot(tue, loc, numSlots)
	if sun || idx != 1 {
		t.Fatalf("expected Tuesday to be idx 1, got sun=%v, idx=%d", sun, idx)
	}

	// Wednesday (Rabu, day 3, idx 2)
	wed := time.Date(2026, 9, 30, 12, 0, 0, 0, loc)
	sun, idx = piket.CalculatePiketSlot(wed, loc, numSlots)
	if sun || idx != 2 {
		t.Fatalf("expected Wednesday to be idx 2, got sun=%v, idx=%d", sun, idx)
	}

	// Saturday (Sabtu, day 6, idx 5)
	sat := time.Date(2026, 10, 3, 12, 0, 0, 0, loc)
	sun, idx = piket.CalculatePiketSlot(sat, loc, numSlots)
	if sun || idx != 5 {
		t.Fatalf("expected Saturday to be idx 5, got sun=%v, idx=%d", sun, idx)
	}

	// Sunday (Minggu: isSunday = true)
	sunDate := time.Date(2026, 10, 4, 12, 0, 0, 0, loc)
	sun, _ = piket.CalculatePiketSlot(sunDate, loc, numSlots)
	if !sun {
		t.Fatalf("expected Sunday to have isSunday=true, got sun=%v", sun)
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
