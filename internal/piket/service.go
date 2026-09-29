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

// DeleteSlotByQuery deletes a slot matching:
// 1. Tagged members (if mentionedJIDs provided)
// 2. Slot number (e.g. "1" for slot #1)
// 3. Database ID
func (s *Service) DeleteSlotByQuery(ctx context.Context, query string, mentionedJIDs []string) (*Slot, error) {
	slots, err := s.repo.ListActiveSlots(ctx)
	if err != nil {
		return nil, err
	}
	if len(slots) == 0 {
		return nil, ErrNoSlotsConfigured
	}

	var targetSlot *Slot

	// 1. Try finding by mentioned JIDs or phone
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

	// 2. If not found by mention, try parsing query as slot number (rotation_order) or ID
	if targetSlot == nil && strings.TrimSpace(query) != "" {
		trimmed := strings.TrimPrefix(strings.TrimSpace(query), "#")
		var num int
		if _, err := fmt.Sscanf(trimmed, "%d", &num); err == nil {
			// First try by RotationOrder (what user sees: Slot #1, #2, etc.)
			for i := range slots {
				if slots[i].RotationOrder == num {
					targetSlot = &slots[i]
					break
				}
			}
			// Fallback by database ID
			if targetSlot == nil {
				for i := range slots {
					if slots[i].ID == int64(num) {
						targetSlot = &slots[i]
						break
					}
				}
			}
		}
	}

	if targetSlot == nil {
		return nil, fmt.Errorf("slot piket tidak ditemukan. Ketik `/listpiket` untuk melihat daftar slot")
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

// GetTodaySlotAndLog returns today's assigned slot and log (creates log if not yet created).
func (s *Service) GetTodaySlotAndLog(ctx context.Context, now time.Time) (*Log, *Slot, error) {
	loc := s.cfg.AppTimezone
	if loc == nil {
		loc = time.Local
	}
	localNow := now.In(loc)
	dateStr := localNow.Format("2006-01-02")

	slots, err := s.repo.ListActiveSlots(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list slots: %w", err)
	}
	if len(slots) == 0 {
		return nil, nil, ErrNoSlotsConfigured
	}

	// Calculate deterministic daily rotation index
	// Day offset since unix epoch in local timezone
	dayCount := int(localNow.Unix() / 86400)
	slotIndex := ((dayCount % len(slots)) + len(slots)) % len(slots)
	assignedSlot := &slots[slotIndex]

	logRecord, err := s.repo.GetLogByDate(ctx, dateStr)
	if err != nil {
		return nil, nil, err
	}

	if logRecord == nil {
		// Build assigned members display string
		var names []string
		for _, m := range assignedSlot.Members {
			names = append(names, fmt.Sprintf("@%s (%s)", m.Name, m.PhoneNumber))
		}
		display := strings.Join(names, " & ")

		newLog := &Log{
			FeedingDate:            dateStr,
			SlotID:                 &assignedSlot.ID,
			AssignedMembersDisplay: display,
			Status:                 StatusPending,
		}
		if err := s.repo.CreateLog(ctx, newLog); err != nil {
			return nil, nil, fmt.Errorf("failed to create today piket log: %w", err)
		}
		logRecord = newLog
	}

	return logRecord, assignedSlot, nil
}

// GetWeeklySchedule returns the calculated roster for the next 7 days.
type DaySchedule struct {
	Date     string
	DayName  string
	SlotName string
	Members  []Member
	IsToday  bool
}

func (s *Service) GetWeeklySchedule(ctx context.Context, now time.Time) ([]DaySchedule, error) {
	loc := s.cfg.AppTimezone
	if loc == nil {
		loc = time.Local
	}
	localNow := now.In(loc)

	slots, err := s.repo.ListActiveSlots(ctx)
	if err != nil {
		return nil, err
	}
	if len(slots) == 0 {
		return nil, ErrNoSlotsConfigured
	}

	dayNames := map[time.Weekday]string{
		time.Sunday:    "Minggu",
		time.Monday:    "Senin",
		time.Tuesday:   "Selasa",
		time.Wednesday: "Rabu",
		time.Thursday:  "Kamis",
		time.Friday:    "Jumat",
		time.Saturday:  "Sabtu",
	}

	var schedule []DaySchedule
	for i := 0; i < 7; i++ {
		targetDate := localNow.AddDate(0, 0, i)
		dayCount := int(targetDate.Unix() / 86400)
		slotIndex := ((dayCount % len(slots)) + len(slots)) % len(slots)
		slot := slots[slotIndex]

		schedule = append(schedule, DaySchedule{
			Date:     targetDate.Format("2006-01-02"),
			DayName:  dayNames[targetDate.Weekday()],
			SlotName: slot.Name,
			Members:  slot.Members,
			IsToday:  (i == 0),
		})
	}

	return schedule, nil
}

// SwapTodayPiket overrides today's assigned members (for when someone is unavailable).
func (s *Service) SwapTodayPiket(ctx context.Context, now time.Time, newDisplayName string) error {
	loc := s.cfg.AppTimezone
	if loc == nil {
		loc = time.Local
	}
	dateStr := now.In(loc).Format("2006-01-02")

	// Ensure today's log exists
	_, _, err := s.GetTodaySlotAndLog(ctx, now)
	if err != nil {
		return err
	}

	return s.repo.UpdateAssignedMembers(ctx, dateStr, newDisplayName)
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
