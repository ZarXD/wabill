package piket

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wabill/internal/config"
)

var (
	ErrPiketAlreadyDone    = errors.New("piket lele hari ini sudah selesai dikonfirmasi")
	ErrNoSlotsConfigured   = errors.New("belum ada jadwal slot piket lele yang terdaftar")
	ErrUnauthorizedFeeder  = errors.New("bukan petugas piket hari ini dan tidak menyertakan caption #pakan")
	ErrInvalidPhotoMedia   = errors.New("file bukti harus berupa gambar/foto")
)

type Service struct {
	cfg  *config.Config
	repo *Repo
}

func NewService(cfg *config.Config, repo *Repo) *Service {
	return &Service{
		cfg:  cfg,
		repo: repo,
	}
}

// RegisterSlot registers a new slot (Solo: 1 member, Duet: 2 members).
func (s *Service) RegisterSlot(ctx context.Context, name string, members []Member) (*Slot, error) {
	if len(members) == 0 {
		return nil, errors.New("minimal 1 anggota untuk mendaftarkan slot piket")
	}

	if strings.TrimSpace(name) == "" {
		var names []string
		for _, m := range members {
			names = append(names, m.Name)
		}
		name = strings.Join(names, " & ")
	}

	return s.repo.CreateSlot(ctx, name, members)
}

// ListSlots returns all active slots with their members.
func (s *Service) ListSlots(ctx context.Context) ([]Slot, error) {
	return s.repo.ListActiveSlots(ctx)
}

// DeleteSlot deletes a slot by ID.
func (s *Service) DeleteSlot(ctx context.Context, slotID int64) error {
	return s.repo.DeleteSlot(ctx, slotID)
}

// ParseDayOfWeek parses an Indonesian/English day name or number into time.Weekday and standard Indonesian name.
// Returns weekday (1=Monday ... 6=Saturday, 0=Sunday), localized name, and true if valid.
func ParseDayOfWeek(input string) (time.Weekday, string, bool) {
	lower := strings.ToLower(strings.TrimSpace(input))
	lower = strings.TrimPrefix(lower, "#")
	switch lower {
	case "senin", "senen", "mon", "monday", "1":
		return time.Monday, "Senin", true
	case "selasa", "tue", "tuesday", "2":
		return time.Tuesday, "Selasa", true
	case "rabu", "rebo", "wed", "wednesday", "3":
		return time.Wednesday, "Rabu", true
	case "kamis", "kemis", "thu", "thursday", "4":
		return time.Thursday, "Kamis", true
	case "jumat", "jum'at", "jumaat", "fri", "friday", "5":
		return time.Friday, "Jumat", true
	case "sabtu", "sat", "saturday", "6":
		return time.Saturday, "Sabtu", true
	case "minggu", "ahad", "sun", "sunday", "7", "0":
		return time.Sunday, "Minggu", true
	default:
		return 0, "", false
	}
}

// SetDaySlot assigns one or more members to a specific day of week (1=Monday ... 6=Saturday).
func (s *Service) SetDaySlot(ctx context.Context, dayOrder int, dayName string, members []Member) (*Slot, error) {
	if len(members) == 0 {
		return nil, errors.New("minimal 1 anggota untuk mendaftarkan jadwal piket")
	}
	if dayOrder < 1 || dayOrder > 6 {
		return nil, errors.New("hari tidak valid. Pilihan: Senin, Selasa, Rabu, Kamis, Jumat, Sabtu (Minggu adalah Piket Bersama)")
	}
	return s.repo.SetSlotForDay(ctx, dayOrder, dayName, members)
}

// DeleteSlotByDay deletes the slot for a specific day of week.
func (s *Service) DeleteSlotByDay(ctx context.Context, dayOrder int) error {
	return s.repo.DeleteSlotByDay(ctx, dayOrder)
}

// DeleteSlotByQuery deletes a slot matching:
// 1. Day name (e.g. "senin", "jumat")
// 2. Tagged members (if mentionedJIDs provided)
// 3. Slot number or ID
func (s *Service) DeleteSlotByQuery(ctx context.Context, query string, mentionedJIDs []string) (*Slot, error) {
	// 1. Check if query is a day of week
	if strings.TrimSpace(query) != "" {
		if wd, dayName, ok := ParseDayOfWeek(query); ok {
			if wd == time.Sunday {
				return nil, errors.New("hari Minggu adalah jadwal Piket Bersama dan tidak memiliki slot individu untuk dihapus")
			}
			dayOrder := int(wd)
			slot, err := s.repo.GetSlotByDay(ctx, dayOrder)
			if err != nil {
				return nil, err
			}

			// Delete from piket_slots
			_ = s.repo.DeleteSlotByDay(ctx, dayOrder)

			// Also reset any pending log for this day
			loc := s.cfg.AppTimezone
			if loc == nil {
				loc = time.Local
			}
			today := time.Now().In(loc)
			diff := int(wd) - int(today.Weekday())
			targetDateStr := today.AddDate(0, 0, diff).Format("2006-01-02")
			_ = s.repo.UpdateAssignedMembers(ctx, targetDateStr, "Belum diatur")

			if slot == nil {
				return &Slot{Name: dayName}, nil
			}
			return slot, nil
		}
	}

	slots, err := s.repo.ListActiveSlots(ctx)
	if err != nil {
		return nil, err
	}
	if len(slots) == 0 {
		return nil, ErrNoSlotsConfigured
	}

	var targetSlot *Slot

	// 2. Try finding by mentioned JIDs or phone
	if len(mentionedJIDs) > 0 {
		for _, jid := range mentionedJIDs {
			user := jid
			if atIdx := strings.Index(jid, "@"); atIdx != -1 {
				user = jid[:atIdx]
			}
			cleanUser := config.NormalizePhone(user)
			for i := range slots {
				for _, m := range slots[i].Members {
					mClean := config.NormalizePhone(m.PhoneNumber)
					if m.WhatsAppJID == jid || m.PhoneNumber == user || (cleanUser != "" && mClean == cleanUser) {
						targetSlot = &slots[i]
						break
					}
				}
				if targetSlot != nil {
					break
				}
			}
			if targetSlot != nil {
				break
			}
		}
	}

	// 3. If not found by mention, try parsing query as day order / slot number or ID
	if targetSlot == nil && strings.TrimSpace(query) != "" {
		trimmed := strings.TrimPrefix(strings.TrimSpace(query), "#")
		var num int
		if _, err := fmt.Sscanf(trimmed, "%d", &num); err == nil {
			for i := range slots {
				if slots[i].RotationOrder == num || slots[i].ID == int64(num) {
					targetSlot = &slots[i]
					break
				}
			}
		}
	}

	if targetSlot == nil {
		return nil, fmt.Errorf("jadwal piket tidak ditemukan. Ketik `/piket` untuk melihat jadwal")
	}

	if err := s.repo.DeleteSlot(ctx, targetSlot.ID); err != nil {
		return nil, err
	}

	return targetSlot, nil
}

// ResetAllSlots deletes all piket slots.
func (s *Service) ResetAllSlots(ctx context.Context) error {
	return s.repo.ResetAllSlots(ctx)
}

// CalculatePiketSlot computes the slot index for a given date, exempting Sundays.
func CalculatePiketSlot(targetDate time.Time, loc *time.Location, numSlots int) (bool, int) {
	if numSlots <= 0 {
		return false, 0
	}
	if loc == nil {
		loc = time.Local
	}
	local := targetDate.In(loc)
	if local.Weekday() == time.Sunday {
		return true, 0
	}
	// With fixed days, day order is weekday (1=Monday ... 6=Saturday)
	idx := (int(local.Weekday()) - 1) % numSlots
	return false, idx
}

// GetSlotAndLogForDate returns the assigned slot and log for a given date (creates log if not yet created).
func (s *Service) GetSlotAndLogForDate(ctx context.Context, targetDate time.Time) (*Log, *Slot, error) {
	loc := s.cfg.AppTimezone
	if loc == nil {
		loc = time.Local
	}
	localTarget := targetDate.In(loc)
	dateStr := localTarget.Format("2006-01-02")
	weekday := localTarget.Weekday()

	isSunday := (weekday == time.Sunday)
	var assignedSlot *Slot
	var err error

	if !isSunday {
		assignedSlot, err = s.repo.GetSlotByDay(ctx, int(weekday))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get slot for day: %w", err)
		}
	}

	logRecord, err := s.repo.GetLogByDate(ctx, dateStr)
	if err != nil {
		return nil, nil, err
	}

	var slotID *int64
	display := "Semua Anggota (Piket Bersama 🐟✨)"
	if !isSunday {
		if assignedSlot != nil && len(assignedSlot.Members) > 0 {
			slotID = &assignedSlot.ID
			var names []string
			for _, m := range assignedSlot.Members {
				user := m.PhoneNumber
				if m.WhatsAppJID != "" {
					if parts := strings.Split(m.WhatsAppJID, "@"); len(parts) > 0 && parts[0] != "" {
						user = parts[0]
					}
				}
				names = append(names, "@"+user)
			}
			display = strings.Join(names, " & ")
		} else {
			display = "Belum diatur"
		}
	}

	if logRecord == nil {
		newLog := &Log{
			FeedingDate:            dateStr,
			SlotID:                 slotID,
			AssignedMembersDisplay: display,
			Status:                 StatusPending,
		}
		if err := s.repo.CreateLog(ctx, newLog); err != nil {
			return nil, nil, fmt.Errorf("failed to create piket log: %w", err)
		}
		logRecord = newLog
	} else if logRecord.Status == StatusPending {
		// Sync log with current slot configuration if it hasn't been fed yet
		if assignedSlot == nil && !isSunday && logRecord.AssignedMembersDisplay != "Belum diatur" {
			logRecord.SlotID = nil
			logRecord.AssignedMembersDisplay = "Belum diatur"
			_ = s.repo.UpdateAssignedMembers(ctx, dateStr, "Belum diatur")
		} else if isSunday && logRecord.SlotID != nil {
			logRecord.SlotID = nil
			logRecord.AssignedMembersDisplay = "Semua Anggota (Piket Bersama 🐟✨)"
			_ = s.repo.UpdateAssignedMembers(ctx, dateStr, logRecord.AssignedMembersDisplay)
		} else if assignedSlot != nil && (logRecord.SlotID == nil || *logRecord.SlotID != assignedSlot.ID) {
			logRecord.SlotID = &assignedSlot.ID
			logRecord.AssignedMembersDisplay = display
			_ = s.repo.UpdateAssignedMembers(ctx, dateStr, display)
		}
	}

	return logRecord, assignedSlot, nil
}

func (s *Service) GetTodaySlotAndLog(ctx context.Context, now time.Time) (*Log, *Slot, error) {
	return s.GetSlotAndLogForDate(ctx, now)
}

// GetWeeklySchedule returns the fixed weekly roster for Senin s/d Minggu.
type DaySchedule struct {
	Date          string
	DayName       string
	SlotName      string
	Members       []Member
	CustomDisplay string
	IsToday       bool
	IsSunday      bool
}

func (s *Service) GetWeeklySchedule(ctx context.Context, now time.Time) ([]DaySchedule, error) {
	loc := s.cfg.AppTimezone
	if loc == nil {
		loc = time.Local
	}
	localNow := now.In(loc)
	todayWeekday := localNow.Weekday()

	slots, err := s.repo.ListActiveSlots(ctx)
	if err != nil {
		return nil, err
	}

	slotMap := make(map[int]Slot)
	for _, sl := range slots {
		slotMap[sl.RotationOrder] = sl
	}

	dayConfigs := []struct {
		weekday time.Weekday
		order   int
		name    string
	}{
		{time.Monday, 1, "Senin"},
		{time.Tuesday, 2, "Selasa"},
		{time.Wednesday, 3, "Rabu"},
		{time.Thursday, 4, "Kamis"},
		{time.Friday, 5, "Jumat"},
		{time.Saturday, 6, "Sabtu"},
		{time.Sunday, 7, "Minggu"},
	}

	var schedule []DaySchedule
	for _, dc := range dayConfigs {
		isSunday := (dc.weekday == time.Sunday)
		isToday := (dc.weekday == todayWeekday)

		diff := int(dc.weekday) - int(todayWeekday)
		targetDate := localNow.AddDate(0, 0, diff)
		targetDateStr := targetDate.Format("2006-01-02")

		var slotName string
		var members []Member
		customDisplay := ""

		if isSunday {
			slotName = "Piket Bersama"
		} else {
			if sl, ok := slotMap[dc.order]; ok {
				slotName = sl.Name
				members = sl.Members

				// Only check customDisplay if slot is configured and was swapped for this date
				logRecord, _ := s.repo.GetLogByDate(ctx, targetDateStr)
				if logRecord != nil && logRecord.AssignedMembersDisplay != "" && logRecord.AssignedMembersDisplay != "Belum diatur" {
					// Check if display differs from default
					var names []string
					for _, m := range members {
						user := m.PhoneNumber
						if m.WhatsAppJID != "" {
							if parts := strings.Split(m.WhatsAppJID, "@"); len(parts) > 0 && parts[0] != "" {
								user = parts[0]
							}
						}
						names = append(names, "@"+user)
					}
					defaultDisplay := strings.Join(names, " & ")
					if logRecord.AssignedMembersDisplay != defaultDisplay {
						customDisplay = logRecord.AssignedMembersDisplay
					}
				}
			} else {
				slotName = "Belum diatur"
				customDisplay = ""
			}
		}

		schedule = append(schedule, DaySchedule{
			Date:          targetDateStr,
			DayName:       dc.name,
			SlotName:      slotName,
			Members:       members,
			CustomDisplay: customDisplay,
			IsToday:       isToday,
			IsSunday:      isSunday,
		})
	}

	return schedule, nil
}

// SwapDatePiket overrides assigned members for a specific target date.
func (s *Service) SwapDatePiket(ctx context.Context, targetTime time.Time, newDisplayName string) error {
	loc := s.cfg.AppTimezone
	if loc == nil {
		loc = time.Local
	}
	dateStr := targetTime.In(loc).Format("2006-01-02")

	// Ensure log exists for that date
	_, _, err := s.GetSlotAndLogForDate(ctx, targetTime)
	if err != nil {
		return err
	}

	return s.repo.UpdateAssignedMembers(ctx, dateStr, newDisplayName)
}

// SwapTodayPiket overrides today's assigned members (for when someone is unavailable).
func (s *Service) SwapTodayPiket(ctx context.Context, now time.Time, newDisplayName string) error {
	return s.SwapDatePiket(ctx, now, newDisplayName)
}

// SubmitPhotoProof handles an incoming photo in the group and verifies anti-spam rules.
func (s *Service) SubmitPhotoProof(
	ctx context.Context,
	senderJID, senderPhone, caption string,
	imgBytes []byte,
	now time.Time,
) (*Log, *Slot, error) {
	logRecord, slot, err := s.GetTodaySlotAndLog(ctx, now)
	if err != nil {
		return nil, nil, err
	}

	// 1. Anti-spam Check 1: Is today already DONE?
	if logRecord.Status == StatusDone {
		return nil, nil, ErrPiketAlreadyDone
	}

	// 2. Anti-spam Check 2: Who is sending this?
	// Must be either:
	// a. One of today's assigned slot members
	// b. An admin
	// c. Explicit caption contains #pakan or pakan
	cleanSenderPhone := config.NormalizePhone(senderPhone)
	if cleanSenderPhone == "" {
		cleanSenderPhone = config.NormalizePhone(senderJID)
	}

	isAssigned := false
	if slot != nil {
		for _, m := range slot.Members {
			cleanMPhone := config.NormalizePhone(m.PhoneNumber)
			if cleanSenderPhone != "" && cleanMPhone != "" && cleanMPhone == cleanSenderPhone {
				isAssigned = true
				break
			}
			cleanMJID := config.NormalizePhone(m.WhatsAppJID)
			if cleanSenderPhone != "" && cleanMJID != "" && cleanMJID == cleanSenderPhone {
				isAssigned = true
				break
			}
			if strings.Contains(senderJID, "@lid") {
				lidUser := strings.Split(senderJID, "@")[0]
				if strings.Contains(m.WhatsAppJID, lidUser) || strings.Contains(m.PhoneNumber, lidUser) {
					isAssigned = true
					break
				}
			}
		}
	}

	if !isAssigned && logRecord != nil && logRecord.AssignedMembersDisplay != "" {
		if strings.Contains(logRecord.AssignedMembersDisplay, "Piket Bersama") {
			isAssigned = true
		} else {
			if cleanSenderPhone != "" && strings.Contains(logRecord.AssignedMembersDisplay, cleanSenderPhone) {
				isAssigned = true
			}
			if strings.Contains(senderJID, "@lid") {
				lidUser := strings.Split(senderJID, "@")[0]
				if strings.Contains(logRecord.AssignedMembersDisplay, lidUser) {
					isAssigned = true
				}
			}
		}
	}

	isAdmin := s.cfg.IsAdmin(senderJID, senderPhone)

	if !isAssigned && !isAdmin {
		return nil, nil, ErrUnauthorizedFeeder
	}

	// 3. Store proof photo safely
	storageDir := s.cfg.LeleProofStoragePath
	if storageDir == "" {
		storageDir = "./data/piket-proofs"
	}
	if err := os.MkdirAll(storageDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create piket proof directory: %w", err)
	}

	randBytes := make([]byte, 4)
	_, _ = rand.Read(randBytes)
	fileName := fmt.Sprintf("piket_%s_%s.jpg", logRecord.FeedingDate, hex.EncodeToString(randBytes))
	filePath := filepath.Join(storageDir, fileName)

	if err := os.WriteFile(filePath, imgBytes, 0644); err != nil {
		return nil, nil, fmt.Errorf("failed to save proof photo: %w", err)
	}

	confirmedBy := cleanSenderPhone
	if senderPhone != "" {
		confirmedBy = senderPhone
	}

	// 4. Mark DONE in database
	fedAt := now.UTC()
	if err := s.repo.MarkDone(ctx, logRecord.FeedingDate, confirmedBy, filePath, fedAt); err != nil {
		return nil, nil, fmt.Errorf("failed to mark piket as done: %w", err)
	}

	logRecord.Status = StatusDone
	logRecord.FedAt = &fedAt
	logRecord.ProofFilePath = filePath
	logRecord.ConfirmedBy = confirmedBy

	return logRecord, slot, nil
}

// MarkDoneManually marks today's feeding as DONE via text command (e.g. "sudah").
func (s *Service) MarkDoneManually(ctx context.Context, senderJID, senderPhone string, now time.Time) (*Log, *Slot, error) {
	logRecord, slot, err := s.GetTodaySlotAndLog(ctx, now)
	if err != nil {
		return nil, nil, err
	}

	if logRecord.Status == StatusDone {
		return nil, nil, ErrPiketAlreadyDone
	}

	fedAt := now.UTC()
	confirmedBy := senderPhone
	if confirmedBy == "" {
		confirmedBy = senderJID
	}

	if err := s.repo.MarkDone(ctx, logRecord.FeedingDate, confirmedBy, "", fedAt); err != nil {
		return nil, nil, fmt.Errorf("failed to mark piket as done: %w", err)
	}

	logRecord.Status = StatusDone
	logRecord.FedAt = &fedAt
	logRecord.ConfirmedBy = confirmedBy

	return logRecord, slot, nil
}

// CheckAndSendReminders evaluates if any of the 3-tiered reminders are due.
func (s *Service) CheckAndSendReminders(
	ctx context.Context,
	now time.Time,
	sendReminder func(reminderType string, slot *Slot, logRecord *Log) error,
) error {
	loc := s.cfg.AppTimezone
	if loc == nil {
		loc = time.Local
	}
	localNow := now.In(loc)
	currentTimeStr := localNow.Format("15:04")

	logRecord, slot, err := s.GetTodaySlotAndLog(ctx, now)
	if err != nil {
		return err
	}

	// If already DONE, auto-cancel all reminders!
	if logRecord.Status == StatusDone {
		return nil
	}

	earlyTime := s.cfg.LeleEarlyReminderTime
	if earlyTime == "" {
		earlyTime = "15:00"
	}
	feedingTime := s.cfg.LeleFeedingTime
	if feedingTime == "" {
		feedingTime = "16:30"
	}
	overdueTime := s.cfg.LeleOverdueTime
	if overdueTime == "" {
		overdueTime = "17:30"
	}

	// 1. Tier 1: Early Reminder (Warm-up, default 15:00)
	if currentTimeStr >= earlyTime && currentTimeStr < feedingTime && logRecord.EarlyRemindedAt == nil {
		if err := sendReminder("EARLY", slot, logRecord); err == nil {
			_ = s.repo.SetEarlyReminded(ctx, logRecord.FeedingDate)
		}
		return nil
	}

	// 2. Tier 2: Feeding Time Reminder (On-time, default 16:30)
	if currentTimeStr >= feedingTime && currentTimeStr < overdueTime && logRecord.FeedingRemindedAt == nil {
		if err := sendReminder("FEEDING", slot, logRecord); err == nil {
			_ = s.repo.SetFeedingReminded(ctx, logRecord.FeedingDate)
		}
		return nil
	}

	// 3. Tier 3: Overdue Reminder (default 17:30)
	if currentTimeStr >= overdueTime && logRecord.OverdueRemindedAt == nil && logRecord.Status == StatusPending {
		if err := sendReminder("OVERDUE", slot, logRecord); err == nil {
			_ = s.repo.SetOverdueReminded(ctx, logRecord.FeedingDate)
		}
		return nil
	}

	return nil
}
