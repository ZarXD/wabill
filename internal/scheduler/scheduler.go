package scheduler

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"wabill/internal/billing"
	"wabill/internal/config"
	"wabill/internal/domain"
	"wabill/internal/piket"
	"wabill/internal/whatsapp"
)

type Scheduler struct {
	cfg            *config.Config
	billingService *billing.Service
	piketService   *piket.Service
	waClient       whatsapp.WhatsAppClient
	stopChan       chan struct{}
	wg             sync.WaitGroup
}

func NewScheduler(
	cfg *config.Config,
	billingSvc *billing.Service,
	piketSvc *piket.Service,
	waClient whatsapp.WhatsAppClient,
) *Scheduler {
	return &Scheduler{
		cfg:            cfg,
		billingService: billingSvc,
		piketService:   piketSvc,
		waClient:       waClient,
		stopChan:       make(chan struct{}),
	}
}

// Start launches the background ticker loops for expiration, billing reminders, and piket reminders.
func (s *Scheduler) Start() {
	s.wg.Add(3)
	go s.runExpirationWorker()
	go s.runReminderWorker()
	go s.runPiketWorker()
	log.Println("[Scheduler] Background workers started (invoice expiration, billing reminders & lele piket)")
}

// Stop gracefully signals background workers to stop and waits for completion.
func (s *Scheduler) Stop() {
	close(s.stopChan)
	s.wg.Wait()
	log.Println("[Scheduler] Background workers stopped cleanly")
}

func (s *Scheduler) runExpirationWorker() {
	defer s.wg.Done()
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			count, err := s.billingService.ProcessExpiredInvoices(ctx)
			cancel()
			if err != nil {
				log.Printf("[Scheduler] Error checking expired invoices: %v", err)
			} else if count > 0 {
				log.Printf("[Scheduler] Marked %d overdue invoices as EXPIRED", count)
			}
		}
	}
}

func (s *Scheduler) runReminderWorker() {
	defer s.wg.Done()

	// Run reminder check once on startup (after a brief 5s warmup delay)
	select {
	case <-s.stopChan:
		return
	case <-time.After(5 * time.Second):
		s.checkReminders()
	}

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.checkReminders()
		}
	}
}

func (s *Scheduler) checkReminders() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	err := s.billingService.CheckAndSendDueReminders(ctx, func(sub *domain.Subscriber, plan *domain.Plan, subscription *domain.Subscription, daysLeft int) error {
		if !s.waClient.IsConnected() {
			log.Println("[Scheduler] WhatsApp client not connected, skipping reminder")
			return nil
		}

		planName := "Shared Plan"
		price := int64(0)
		if plan != nil {
			planName = plan.Name
			price = plan.Price
		}

		reminderMsg := whatsapp.TemplateReminder(sub.Name, planName, subscription.ExpiresAt, price, s.cfg.AppTimezone)
		buttons := []whatsapp.ButtonOption{
			{ID: "btn_bayar", Text: "💳 Bayar Sekarang"},
		}

		log.Printf("[Scheduler] Sending billing reminder to %s (%s)", sub.Name, sub.WhatsAppJID)
		return s.waClient.SendButtons(ctx, sub.WhatsAppJID, reminderMsg, buttons)
	})

	if err != nil {
		log.Printf("[Scheduler] Error checking due reminders: %v", err)
	}
}

func (s *Scheduler) runPiketWorker() {
	defer s.wg.Done()

	// Brief initial delay on startup
	select {
	case <-s.stopChan:
		return
	case <-time.After(10 * time.Second):
		s.checkPiketReminders()
	}

	// Check every 30 seconds for accurate minute matching
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.checkPiketReminders()
		}
	}
}

func (s *Scheduler) checkPiketReminders() {
	if s.piketService == nil || s.cfg.LeleGroupJID == "" {
		return
	}
	if !s.waClient.IsConnected() {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := s.piketService.CheckAndSendReminders(ctx, time.Now(), func(reminderType string, slot *piket.Slot, logRecord *piket.Log) error {
		var mentions []string
		membersDisplay := ""
		if logRecord != nil && logRecord.AssignedMembersDisplay != "" {
			membersDisplay = logRecord.AssignedMembersDisplay
			for _, part := range strings.Fields(membersDisplay) {
				if strings.HasPrefix(part, "@") {
					user := strings.TrimPrefix(part, "@")
					jid := user + "@s.whatsapp.net"
					if len(user) == 14 || len(user) == 15 {
						jid = user + "@lid"
					}
					mentions = append(mentions, jid)
				}
			}
		} else if slot != nil {
			var displayNames []string
			for _, m := range slot.Members {
				mentions = append(mentions, m.WhatsAppJID)
				displayNames = append(displayNames, "@"+m.PhoneNumber)
			}
			membersDisplay = strings.Join(displayNames, " & ")
		}

		var msg string
		isSunday := (slot == nil) || (logRecord != nil && strings.Contains(logRecord.AssignedMembersDisplay, "Piket Bersama"))
		if isSunday {
			switch reminderType {
			case "EARLY":
				msg = whatsapp.TemplatePiketSundayEarlyReminder(s.cfg.LeleFeedingTime, s.cfg.AppTimezone)
			case "FEEDING":
				msg = whatsapp.TemplatePiketSundayFeedingReminder()
			case "OVERDUE":
				msg = whatsapp.TemplatePiketSundayOverdueReminder()
			default:
				return nil
			}
		} else {
			slotName := "Slot Piket"
			if slot != nil {
				slotName = slot.Name
			}
			switch reminderType {
			case "EARLY":
				msg = whatsapp.TemplatePiketEarlyReminder(slotName, membersDisplay, s.cfg.LeleFeedingTime, s.cfg.AppTimezone)
			case "FEEDING":
				msg = whatsapp.TemplatePiketFeedingReminder(membersDisplay)
			case "OVERDUE":
				msg = whatsapp.TemplatePiketOverdueReminder(membersDisplay)
			default:
				return nil
			}
		}

		log.Printf("[Scheduler] Sending Piket Lele reminder (%s) to group %s (Sunday=%v) for %s", reminderType, s.cfg.LeleGroupJID, isSunday, membersDisplay)
		return s.waClient.SendTextWithMentions(ctx, s.cfg.LeleGroupJID, msg, mentions)
	})

	if err != nil {
		log.Printf("[Scheduler] Error checking piket reminders: %v", err)
	}
}

