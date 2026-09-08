package scheduler

import (
	"context"
	"log"
	"sync"
	"time"

	"wabill/internal/billing"
	"wabill/internal/config"
	"wabill/internal/domain"
	"wabill/internal/whatsapp"
)

type Scheduler struct {
	cfg            *config.Config
	billingService *billing.Service
	waClient       whatsapp.WhatsAppClient
	stopChan       chan struct{}
	wg             sync.WaitGroup
}

func NewScheduler(
	cfg *config.Config,
	billingSvc *billing.Service,
	waClient whatsapp.WhatsAppClient,
) *Scheduler {
	return &Scheduler{
		cfg:            cfg,
		billingService: billingSvc,
		waClient:       waClient,
		stopChan:       make(chan struct{}),
	}
}

// Start launches the background ticker loops for expiration and reminders.
func (s *Scheduler) Start() {
	s.wg.Add(2)
	go s.runExpirationWorker()
	go s.runReminderWorker()
	log.Println("[Scheduler] Background workers started (invoice expiration & billing reminders)")
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
