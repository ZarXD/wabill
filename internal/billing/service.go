package billing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"wabill/internal/config"
	"wabill/internal/domain"
	"wabill/internal/paymentprovider"
	"wabill/internal/storage"
)

type Service struct {
	cfg              *config.Config
	subscriberRepo   *storage.SubscriberRepo
	subscriptionRepo *storage.SubscriptionRepo
	invoiceRepo      *storage.InvoiceRepo
	paymentRepo      *storage.PaymentRepo
	reminderRepo     *storage.ReminderRepo
	auditRepo        *storage.AuditRepo
	paymentProvider  paymentprovider.PaymentProvider
}

func NewService(
	cfg *config.Config,
	subRepo *storage.SubscriberRepo,
	subscriptionRepo *storage.SubscriptionRepo,
	invRepo *storage.InvoiceRepo,
	payRepo *storage.PaymentRepo,
	remRepo *storage.ReminderRepo,
	auditRepo *storage.AuditRepo,
	provider paymentprovider.PaymentProvider,
) *Service {
	return &Service{
		cfg:              cfg,
		subscriberRepo:   subRepo,
		subscriptionRepo: subscriptionRepo,
		invoiceRepo:      invRepo,
		paymentRepo:      payRepo,
		reminderRepo:     remRepo,
		auditRepo:        auditRepo,
		paymentProvider:  provider,
	}
}

type MemberDetails struct {
	Subscriber   *domain.Subscriber
	Plan         *domain.Plan
	Subscription *domain.Subscription
}

// GetOrCreateActiveInvoice retrieves an existing active PENDING invoice or creates a new one.
// Returns (invoice, plan, subscription, isNew, error).
func (s *Service) GetOrCreateActiveInvoice(
	ctx context.Context,
	subscriberJID string,
	pushName string,
) (*domain.Invoice, *domain.Plan, *domain.Subscription, bool, error) {
	phone := config.NormalizePhone(subscriberJID)
	sub, err := s.subscriberRepo.GetSubscriberByPhoneOrJID(ctx, phone, subscriberJID)
	if err != nil || sub.Status != domain.SubscriberActive {
		return nil, nil, nil, false, domain.ErrSubscriberNotFound
	}

	subscription, err := s.subscriptionRepo.GetActiveSubscriptionBySubscriberID(ctx, sub.ID)
	if err != nil {
		subscription, err = s.subscriptionRepo.GetLatestSubscriptionBySubscriberID(ctx, sub.ID)
		if err != nil {
			return nil, nil, nil, false, domain.ErrNoActiveSubscription
		}
	}

	plan, err := s.subscriberRepo.GetPlanByID(ctx, subscription.PlanID)
	if err != nil {
		plan, _ = s.subscriberRepo.GetDefaultPlan(ctx)
	}

	now := time.Now().UTC()

	// Check if active pending invoice already exists (Idempotency!)
	activeInv, err := s.invoiceRepo.GetActiveInvoiceBySubscriberID(ctx, sub.ID)
	if err == nil && activeInv != nil {
		return activeInv, plan, subscription, false, nil
	}

	// Generate invoice number e.g. INV-202609-A8F31
	invNumber := s.generateInvoiceNumber(now)
	billingPeriod := now.Format("2006-01")
	expiresAt := now.Add(time.Duration(s.cfg.InvoiceExpirationMinutes) * time.Minute)

	newInv := &domain.Invoice{
		InvoiceNumber:  invNumber,
		SubscriberID:   sub.ID,
		SubscriptionID: subscription.ID,
		PlanID:         plan.ID,
		BillingPeriod:  billingPeriod,
		Amount:         plan.Price,
		Status:         domain.InvoicePending,
		ExpiresAt:      expiresAt,
	}

	if err := s.invoiceRepo.CreateInvoice(ctx, newInv); err != nil {
		return nil, nil, nil, false, fmt.Errorf("failed to save invoice: %w", err)
	}

	_ = s.auditRepo.Record(ctx, &domain.AuditLog{
		Actor:      subscriberJID,
		Action:     domain.AuditActionInvoiceCreated,
		EntityType: "INVOICE",
		EntityID:   fmt.Sprintf("%d", newInv.ID),
		Metadata:   fmt.Sprintf(`{"invoice_number":"%s","amount":%d}`, newInv.InvoiceNumber, newInv.Amount),
	})

	return newInv, plan, subscription, true, nil
}

// GetCustomerStatus fetches subscriber profile, active plan, and subscription.
func (s *Service) GetCustomerStatus(ctx context.Context, subscriberJID string) (*domain.Subscriber, *domain.Plan, *domain.Subscription, error) {
	phone := config.NormalizePhone(subscriberJID)
	sub, err := s.subscriberRepo.GetSubscriberByPhoneOrJID(ctx, phone, subscriberJID)
	if err != nil || sub.Status != domain.SubscriberActive {
		return nil, nil, nil, domain.ErrSubscriberNotFound
	}

	subscription, err := s.subscriptionRepo.GetActiveSubscriptionBySubscriberID(ctx, sub.ID)
	if err != nil {
		subscription, err = s.subscriptionRepo.GetLatestSubscriptionBySubscriberID(ctx, sub.ID)
		if err != nil {
			return sub, nil, nil, err
		}
	}

	plan, err := s.subscriberRepo.GetPlanByID(ctx, subscription.PlanID)
	if err != nil {
		plan, _ = s.subscriberRepo.GetDefaultPlan(ctx)
	}

	return sub, plan, subscription, nil
}

// GetCustomerHistory fetches recent invoices for customer.
func (s *Service) GetCustomerHistory(ctx context.Context, subscriberJID string, limit int) ([]*domain.Invoice, error) {
	phone := config.NormalizePhone(subscriberJID)
	sub, err := s.subscriberRepo.GetSubscriberByPhoneOrJID(ctx, phone, subscriberJID)
	if err != nil {
		return nil, err
	}
	return s.invoiceRepo.GetRecentInvoicesBySubscriberID(ctx, sub.ID, limit)
}

// GetLatestInvoice gets the most recent invoice for the user regardless of status.
func (s *Service) GetLatestInvoice(ctx context.Context, subscriberJID string) (*domain.Invoice, *domain.Plan, error) {
	phone := config.NormalizePhone(subscriberJID)
	sub, err := s.subscriberRepo.GetSubscriberByPhoneOrJID(ctx, phone, subscriberJID)
	if err != nil {
		return nil, nil, err
	}

	inv, err := s.invoiceRepo.GetLatestInvoiceBySubscriberID(ctx, sub.ID)
	if err != nil {
		return nil, nil, err
	}

	plan, _ := s.subscriberRepo.GetPlanByID(ctx, inv.PlanID)
	return inv, plan, nil
}

// SubmitPaymentProof handles user-uploaded payment proof image.
func (s *Service) SubmitPaymentProof(
	ctx context.Context,
	subscriberJID string,
	fileBytes []byte,
) (*domain.Invoice, *domain.PaymentProof, error) {
	// 1. Verify file size (prevent memory/resource exhaustion before DB lookups)
	maxBytes := s.cfg.MaxProofSizeMB * 1024 * 1024
	if int64(len(fileBytes)) > maxBytes {
		return nil, nil, domain.ErrFileTooLarge
	}

	// 2. Validate MIME type via magic bytes (allow only images)
	detectedMime := http.DetectContentType(fileBytes)
	var ext string
	switch {
	case strings.HasPrefix(detectedMime, "image/jpeg"):
		ext = ".jpg"
	case strings.HasPrefix(detectedMime, "image/png"):
		ext = ".png"
	case strings.HasPrefix(detectedMime, "image/webp"):
		ext = ".webp"
	default:
		return nil, nil, domain.ErrInvalidProofMedia
	}

	phone := config.NormalizePhone(subscriberJID)
	sub, err := s.subscriberRepo.GetSubscriberByPhoneOrJID(ctx, phone, subscriberJID)
	if err != nil || sub.Status != domain.SubscriberActive {
		return nil, nil, domain.ErrSubscriberNotFound
	}

	now := time.Now().UTC()

	// 3. Find invoice that can accept proof
	inv, err := s.invoiceRepo.GetActiveInvoiceBySubscriberID(ctx, sub.ID)
	if err != nil || inv == nil {
		// Check latest invoice to give precise helpful error message
		latest, _ := s.invoiceRepo.GetLatestInvoiceBySubscriberID(ctx, sub.ID)
		if latest != nil {
			if latest.Status == domain.InvoicePendingReview {
				return nil, nil, domain.ErrProofAlreadySubmitted
			}
			if latest.Status == domain.InvoiceExpired || latest.ExpiresAt.Before(now) {
				return nil, nil, domain.ErrInvoiceExpired
			}
		}
		return nil, nil, domain.ErrInvoiceNotFound
	}

	if err := inv.CanAcceptProof(now); err != nil {
		return nil, nil, err
	}

	// 4. Save file to disk
	if err := os.MkdirAll(s.cfg.ProofStoragePath, 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create storage dir: %w", err)
	}

	filename := fmt.Sprintf("%s%s", uuid.New().String(), ext)
	filePath := filepath.Join(s.cfg.ProofStoragePath, filename)
	if err := os.WriteFile(filePath, fileBytes, 0644); err != nil {
		return nil, nil, fmt.Errorf("failed to save proof to disk: %w", err)
	}

	// 5. Update invoice to PENDING_REVIEW
	if err := s.invoiceRepo.UpdateStatus(ctx, inv.ID, domain.InvoicePendingReview); err != nil {
		_ = os.Remove(filePath)
		return nil, nil, fmt.Errorf("failed to transition invoice status: %w", err)
	}
	inv.Status = domain.InvoicePendingReview

	// 6. Record payment proof
	proof := &domain.PaymentProof{
		InvoiceID: inv.ID,
		FilePath:  filePath,
		FileSize:  int64(len(fileBytes)),
		MimeType:  detectedMime,
	}
	if err := s.paymentRepo.SavePaymentProof(ctx, proof); err != nil {
		return nil, nil, fmt.Errorf("failed to save payment proof record: %w", err)
	}

	_ = s.auditRepo.Record(ctx, &domain.AuditLog{
		Actor:      subscriberJID,
		Action:     domain.AuditActionProofSubmitted,
		EntityType: "INVOICE",
		EntityID:   fmt.Sprintf("%d", inv.ID),
		Metadata:   fmt.Sprintf(`{"invoice_number":"%s","file":"%s"}`, inv.InvoiceNumber, filename),
	})

	return inv, proof, nil
}

// ApprovePayment executes atomic payment approval by admin.
func (s *Service) ApprovePayment(
	ctx context.Context,
	invoiceNumber string,
	adminJID string,
) (*domain.Invoice, *domain.Subscription, *domain.Subscriber, *domain.Plan, error) {
	now := time.Now().UTC()

	inv, sub, err := s.paymentRepo.ApproveInvoiceTx(ctx, invoiceNumber, adminJID, now)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	subscriber, err := s.subscriberRepo.GetSubscriberByID(ctx, inv.SubscriberID)
	if err != nil {
		return inv, sub, nil, nil, nil
	}

	plan, _ := s.subscriberRepo.GetPlanByID(ctx, inv.PlanID)
	return inv, sub, subscriber, plan, nil
}

// RejectPayment executes atomic payment rejection by admin.
func (s *Service) RejectPayment(
	ctx context.Context,
	invoiceNumber string,
	adminJID string,
	reason string,
) (*domain.Invoice, *domain.Subscriber, error) {
	now := time.Now().UTC()

	inv, err := s.paymentRepo.RejectInvoiceTx(ctx, invoiceNumber, adminJID, reason, now)
	if err != nil {
		return nil, nil, err
	}

	subscriber, _ := s.subscriberRepo.GetSubscriberByID(ctx, inv.SubscriberID)
	return inv, subscriber, nil
}

func (s *Service) GetPendingReviewInvoices(ctx context.Context) ([]*domain.Invoice, error) {
	return s.invoiceRepo.GetPendingReviewInvoices(ctx)
}

func (s *Service) GetPaymentInstructions(ctx context.Context, amount int64) (*paymentprovider.PaymentInstructions, error) {
	return s.paymentProvider.GetInstructions(ctx, amount)
}

func (s *Service) GetPaymentProof(ctx context.Context, invoiceID int64) (*domain.PaymentProof, error) {
	return s.paymentRepo.GetPaymentProofByInvoiceID(ctx, invoiceID)
}

func (s *Service) GetInvoiceByNumber(ctx context.Context, number string) (*domain.Invoice, *domain.Subscriber, *domain.Plan, error) {
	inv, err := s.invoiceRepo.GetInvoiceByNumber(ctx, number)
	if err != nil {
		return nil, nil, nil, err
	}
	sub, _ := s.subscriberRepo.GetSubscriberByID(ctx, inv.SubscriberID)
	plan, _ := s.subscriberRepo.GetPlanByID(ctx, inv.PlanID)
	return inv, sub, plan, nil
}

func (s *Service) ProcessExpiredInvoices(ctx context.Context) (int64, error) {
	now := time.Now().UTC()
	return s.invoiceRepo.MarkInvoicesExpired(ctx, now)
}

// CheckAndSendDueReminders searches active subscriptions nearing expiry and triggers reminders.
func (s *Service) CheckAndSendDueReminders(
	ctx context.Context,
	sendFn func(sub *domain.Subscriber, plan *domain.Plan, subscription *domain.Subscription, daysLeft int) error,
) error {
	approaching, err := s.subscriptionRepo.GetSubscriptionsApproachingExpiry(ctx, s.cfg.ReminderDaysBeforeExp)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	for _, sub := range approaching {
		billingPeriod := sub.ExpiresAt.Format("2006-01")
		reminderType := "EXPIRATION_APPROACHING"

		alreadySent, err := s.reminderRepo.HasReminderBeenSent(ctx, sub.ID, billingPeriod, reminderType)
		if err != nil || alreadySent {
			continue
		}

		subscriber, err := s.subscriberRepo.GetSubscriberByID(ctx, sub.SubscriberID)
		if err != nil || subscriber.Status != domain.SubscriberActive {
			continue
		}

		plan, err := s.subscriberRepo.GetPlanByID(ctx, sub.PlanID)
		if err != nil {
			continue
		}

		daysLeft := int(sub.ExpiresAt.Sub(now).Hours() / 24)
		if daysLeft < 0 {
			daysLeft = 0
		}

		if err := sendFn(subscriber, plan, sub, daysLeft); err == nil {
			_ = s.reminderRepo.RecordReminder(ctx, &domain.Reminder{
				SubscriptionID: sub.ID,
				BillingPeriod:  billingPeriod,
				ReminderType:   reminderType,
			})
			_ = s.auditRepo.Record(ctx, &domain.AuditLog{
				Actor:      "SYSTEM",
				Action:     domain.AuditActionReminderSent,
				EntityType: "SUBSCRIPTION",
				EntityID:   fmt.Sprintf("%d", sub.ID),
				Metadata:   fmt.Sprintf(`{"subscriber":"%s","days_left":%d}`, subscriber.WhatsAppJID, daysLeft),
			})
		}
	}
	return nil
}

// IsRegisteredSubscriber checks if sender is an active registered subscriber.
func (s *Service) IsRegisteredSubscriber(ctx context.Context, jid, phone string) (*domain.Subscriber, error) {
	cleanPhone := config.NormalizePhone(phone)
	if cleanPhone == "" {
		cleanPhone = config.NormalizePhone(jid)
	}
	sub, err := s.subscriberRepo.GetSubscriberByPhoneOrJID(ctx, cleanPhone, jid)
	if err != nil {
		return nil, err
	}
	if sub.Status != domain.SubscriberActive {
		return nil, domain.ErrSubscriberNotFound
	}
	return sub, nil
}

// ListMembers returns all subscribers with their active plan and current subscription expiry.
func (s *Service) ListMembers(ctx context.Context) ([]MemberDetails, error) {
	subs, err := s.subscriberRepo.GetAllSubscribers(ctx)
	if err != nil {
		return nil, err
	}

	var results []MemberDetails
	for _, sub := range subs {
		subsc, _ := s.subscriptionRepo.GetLatestSubscriptionBySubscriberID(ctx, sub.ID)
		var plan *domain.Plan
		if subsc != nil {
			plan, _ = s.subscriberRepo.GetPlanByID(ctx, subsc.PlanID)
		} else {
			plan, _ = s.subscriberRepo.GetDefaultPlan(ctx)
		}
		results = append(results, MemberDetails{
			Subscriber:   sub,
			Plan:         plan,
			Subscription: subsc,
		})
	}
	return results, nil
}

// AddMember registers a new customer, plan, and initial subscription with fixed cycle day.
func (s *Service) AddMember(ctx context.Context, phone, name, planName string, cycleDay int) (*domain.Subscriber, *domain.Subscription, error) {
	cleanPhone := config.NormalizePhone(phone)
	if cleanPhone == "" {
		return nil, nil, errors.New("nomor telepon tidak valid")
	}

	var plan *domain.Plan
	var err error
	if planName != "" {
		plan, err = s.subscriberRepo.GetPlanByName(ctx, planName)
	}
	if plan == nil || err != nil {
		plan, err = s.subscriberRepo.GetDefaultPlan(ctx)
		if err != nil {
			defaultPlan := &domain.Plan{
				Name:            "Google One Family",
				Description:     "Shared 2TB Google One Subscription",
				Price:           23000,
				BillingInterval: "MONTHLY",
				Active:          true,
			}
			_ = s.subscriberRepo.CreatePlan(ctx, defaultPlan)
			plan = defaultPlan
		}
	}

	if cycleDay <= 0 || cycleDay > 28 {
		cycleDay = 1 // Default to 1st of month
	}

	// Calculate initial expiry aligned to cycleDay
	now := time.Now().UTC()
	targetMonth := now.AddDate(0, 1, 0)
	initialExpiry := time.Date(targetMonth.Year(), targetMonth.Month(), cycleDay, 23, 59, 59, 0, time.UTC)

	// Check if already exists in DB
	sub, err := s.subscriberRepo.GetSubscriberByPhoneOrJID(ctx, cleanPhone, "")
	if err == nil && sub != nil {
		sub.Name = name
		sub.Status = domain.SubscriberActive
		_ = s.subscriberRepo.UpdateSubscriberStatus(ctx, sub.ID, domain.SubscriberActive)
	} else {
		newSub := &domain.Subscriber{
			Name:        name,
			PhoneNumber: cleanPhone,
			WhatsAppJID: cleanPhone + "@s.whatsapp.net",
			Status:      domain.SubscriberActive,
		}
		if err := s.subscriberRepo.CreateSubscriber(ctx, newSub); err != nil {
			return nil, nil, err
		}
		sub = newSub
	}

	// Create or update subscription
	existingSubsc, err := s.subscriptionRepo.GetActiveSubscriptionBySubscriberID(ctx, sub.ID)
	if err == nil && existingSubsc != nil {
		_ = s.subscriptionRepo.UpdateSubscriptionExpiry(ctx, existingSubsc.ID, initialExpiry, domain.SubscriptionActive)
		existingSubsc.ExpiresAt = initialExpiry
		return sub, existingSubsc, nil
	}

	newSubsc := &domain.Subscription{
		SubscriberID: sub.ID,
		PlanID:       plan.ID,
		Status:       domain.SubscriptionActive,
		StartedAt:    now,
		ExpiresAt:    initialExpiry,
	}
	if err := s.subscriptionRepo.CreateSubscription(ctx, newSubsc); err != nil {
		return nil, nil, err
	}

	return sub, newSubsc, nil
}

// DeactivateMember deactivates a subscriber by phone.
func (s *Service) DeactivateMember(ctx context.Context, phone string) error {
	cleanPhone := config.NormalizePhone(phone)
	sub, err := s.subscriberRepo.GetSubscriberByPhoneOrJID(ctx, cleanPhone, "")
	if err != nil {
		return err
	}
	return s.subscriberRepo.UpdateSubscriberStatus(ctx, sub.ID, domain.SubscriberInactive)
}

// ActivateMember reactivates an inactive subscriber by phone.
func (s *Service) ActivateMember(ctx context.Context, phone string) error {
	cleanPhone := config.NormalizePhone(phone)
	sub, err := s.subscriberRepo.GetSubscriberByPhoneOrJID(ctx, cleanPhone, "")
	if err != nil {
		return err
	}
	return s.subscriberRepo.UpdateSubscriberStatus(ctx, sub.ID, domain.SubscriberActive)
}

// SetMemberExpiry adjusts subscriber's expiration date directly.
func (s *Service) SetMemberExpiry(ctx context.Context, phone string, newExpiry time.Time) error {
	cleanPhone := config.NormalizePhone(phone)
	sub, err := s.subscriberRepo.GetSubscriberByPhoneOrJID(ctx, cleanPhone, "")
	if err != nil {
		return err
	}
	subsc, err := s.subscriptionRepo.GetLatestSubscriptionBySubscriberID(ctx, sub.ID)
	if err != nil {
		return err
	}
	return s.subscriptionRepo.UpdateSubscriptionExpiry(ctx, subsc.ID, newExpiry, domain.SubscriptionActive)
}

func (s *Service) generateInvoiceNumber(t time.Time) string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return fmt.Sprintf("INV-%s-%s", t.Format("200601"), strings.ToUpper(hex.EncodeToString(b)))
}

func extractPhoneFromJID(jid string) string {
	parts := strings.Split(jid, "@")
	user := parts[0]
	if idx := strings.Index(user, ":"); idx != -1 {
		user = user[:idx]
	}
	return user
}

func errorsIs(err, target error) bool {
	return err == target || strings.Contains(err.Error(), target.Error())
}
