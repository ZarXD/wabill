package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"wabill/internal/billing"
	"wabill/internal/config"
	"wabill/internal/domain"
	"wabill/internal/piket"
	"wabill/internal/storage"
)

type Router struct {
	cfg            *config.Config
	billingService *billing.Service
	piketService   *piket.Service
	waClient       WhatsAppClient
	dedupRepo      *storage.DeduplicationRepo
	limiter        *UserRateLimiter
	userLock       *KeyedMutex
	supportMgr     *SupportSessionManager
}

func NewRouter(
	cfg *config.Config,
	billingSvc *billing.Service,
	piketSvc *piket.Service,
	waClient WhatsAppClient,
	dedupRepo *storage.DeduplicationRepo,
) *Router {
	supportMgr := NewSupportSessionManager(5 * time.Minute)
	r := &Router{
		cfg:            cfg,
		billingService: billingSvc,
		piketService:   piketSvc,
		waClient:       waClient,
		dedupRepo:      dedupRepo,
		limiter: NewUserRateLimiter(RateLimiterConfig{
			MaxRequests:    5,
			WindowDuration: 10 * time.Second,
			Cooldown:       8 * time.Second,
		}),
		userLock:   NewKeyedMutex(),
		supportMgr: supportMgr,
	}

	// Auto-close notification when a support session times out after 5 minutes of inactivity
	supportMgr.SetOnTimeout(func(s *SupportSession) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		log.Printf("[Router] Support session for %s timed out after 5 minutes of inactivity", s.PhoneNumber)
		_ = waClient.SendText(ctx, s.CustomerJID, TemplateSupportSessionTimeoutCustomer())

		adminNotice := TemplateSupportSessionTimeoutAdmin(s.PushName, s.PhoneNumber)
		for _, adminJID := range cfg.AdminJIDs {
			_ = waClient.SendText(ctx, adminJID, adminNotice)
		}
	})

	return r
}

// Close gracefully stops router background managers.
func (r *Router) Close() {
	if r.supportMgr != nil {
		r.supportMgr.Stop()
	}
}

// HandleEvent is the central whatsmeow event dispatcher.
func (r *Router) HandleEvent(evt any) {
	msgEvt, ok := evt.(*events.Message)
	if !ok {
		return
	}

	// Ignore messages from self or broadcast lists (status updates)
	if msgEvt.Info.IsFromMe || msgEvt.Info.Chat.Server == "broadcast" {
		return
	}

	// Process concurrently in a goroutine so simultaneous chats from different users
	// run in parallel without blocking whatsmeow event loop!
	go r.processMessageEvent(msgEvt)
}

func (r *Router) processMessageEvent(msgEvt *events.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	parsed := ParseIncomingMessage(msgEvt)
	if parsed == nil {
		return
	}

	// 1. Serialize requests from the SAME user using KeyedMutex
	// (Prevents double clicks, simultaneous clicks, race conditions per user)
	userKey := parsed.SenderPhone
	if userKey == "" {
		userKey = parsed.SenderJID
	}
	r.userLock.Lock(userKey)
	defer r.userLock.Unlock(userKey)

	log.Printf("[Router] Received message from Sender=%s (Alt=%s, Chat=%s, Phone=%s, PushName=%q): %q (Cmd=%s)",
		parsed.SenderJID, parsed.SenderAltJID, parsed.ChatJID, parsed.SenderPhone, parsed.PushName, parsed.RawText, parsed.Command)

	// 2. Deduplication check: discard duplicate WhatsApp events
	isNew, err := r.dedupRepo.TryAcquireMessageLock(ctx, parsed.MessageID)
	if err != nil {
		log.Printf("[Router] Dedup check error: %v", err)
	}
	if !isNew {
		log.Printf("[Router] Ignored duplicate message %s", parsed.MessageID)
		return
	}

	// Fast-path: /jid or /cekid command (works anywhere to help configure LELE_GROUP_JID)
	if parsed.Command == CmdJID {
		reply := fmt.Sprintf("🆔 *ID Chat / Grup Ini:*\n`%s`\n\nUntuk menjadikan grup ini sebagai grup piket lele, tambahkan di file `.env`:\n`LELE_GROUP_JID=%s`", parsed.ChatJID, parsed.ChatJID)
		_ = r.waClient.SendText(ctx, parsed.ChatJID, reply)
		return
	}

	isPiketCmd := isPiketCommand(parsed.Command)
	isPiketGroup := r.isLeleGroup(parsed)

	// Auto-detect group: If LELE_GROUP_JID is not configured yet but piket commands are used in a group:
	if parsed.IsGroup && r.cfg.LeleGroupJID == "" && isPiketCmd {
		r.cfg.LeleGroupJID = parsed.ChatJID
		isPiketGroup = true
		log.Printf("[Router] Auto-configured LELE_GROUP_JID=%s from group command %s", parsed.ChatJID, parsed.Command)
	}

	// 3. Customer Whitelist: ONLY respond if sender is an Admin, from Lele Group, or Registered Subscriber!
	// Non-registered numbers (friends, family, casual personal chats) are SILENTLY IGNORED.
	isAdmin := r.isAdminSender(parsed)
	if !isAdmin && !isPiketGroup && !isPiketCmd {
		sub, err := r.billingService.IsRegisteredSubscriber(ctx, parsed.SenderJID, parsed.SenderPhone)
		if err != nil || sub == nil {
			log.Printf("[Router] Ignored message from unregistered sender Sender=%s (Phone=%s)", parsed.SenderJID, parsed.SenderPhone)
			return
		}
	}

	// 4. Mark as read immediately for authorized senders (Centang biru)
	_ = r.waClient.MarkRead(ctx, msgEvt.Info.Chat, msgEvt.Info.Sender, []types.MessageID{msgEvt.Info.ID}, msgEvt.Info.Timestamp)

	// Route to Piket Lele handler if message is from the configured lele group or running a piket command in group
	if isPiketGroup || (parsed.IsGroup && isPiketCmd) {
		r.handleLeleGroupMessage(ctx, parsed)
		return
	}

	// 5. Rate limiting / Anti-spam check (skip for authorized admins)
	if !isAdmin {
		allowed, shouldWarn := r.limiter.CheckLimit(userKey)
		if !allowed {
			log.Printf("[Router] Rate limit triggered for %s", userKey)
			if shouldWarn {
				_ = r.waClient.SendText(ctx, parsed.SenderJID, "⚠️ Kamu mengirim pesan terlalu cepat. Mohon tunggu beberapa detik sebelum mencoba lagi ya 🙏")
			}
			return
		}
	}

	// 5. Check if customer is in an active live support session
	if !isAdmin && r.supportMgr.HasActiveSession(userKey) {
		if strings.TrimSpace(parsed.RawText) == "" && parsed.ImageMessage == nil {
			return
		}

		// 1. Did customer request to close support?
		if parsed.Command == CmdCloseSupport || parsed.ButtonID == "btn_selesai" {
			typingMs := 1000 + rand.Intn(500)
			_ = r.waClient.SimulateTyping(ctx, parsed.SenderJID, time.Duration(typingMs)*time.Millisecond)
			r.handleCloseSupport(ctx, parsed)
			return
		}

		// 2. Did customer try to execute an explicit bot menu or command?
		// Notify them that they are currently in a live support session with the admin.
		if isExplicitBotCommand(parsed) {
			typingMs := 800 + rand.Intn(400)
			_ = r.waClient.SimulateTyping(ctx, parsed.SenderJID, time.Duration(typingMs)*time.Millisecond)
			warnMsg := TemplateSupportSessionActiveWarning()
			buttons := []ButtonOption{
				{ID: "btn_selesai", Text: "✅ Selesai Sesi Bantuan"},
			}
			_ = r.waClient.SendButtons(ctx, parsed.SenderJID, warnMsg, buttons)
			return
		}

		// 3. Forward text, complaint, or image to admin live support
		typingMs := 800 + rand.Intn(600)
		_ = r.waClient.SimulateTyping(ctx, parsed.SenderJID, time.Duration(typingMs)*time.Millisecond)
		r.handleCustomerSupportMessage(ctx, parsed)
		return
	}

	// 6. If not in support session and this is an unrelated chat message (CmdUnknown and not an image), ignore silently
	if parsed.ImageMessage == nil && parsed.Command == CmdUnknown {
		return
	}

	// 7. Simulate human-like typing delay before responding (Anti-ban protection)
	// Random delay between 1200ms and 2500ms
	typingMs := 1200 + rand.Intn(1300)
	if r.isAdminSender(parsed) {
		typingMs = 600 // Snappy response for admin commands
	}
	_ = r.waClient.SimulateTyping(ctx, parsed.SenderJID, time.Duration(typingMs)*time.Millisecond)

	// 8. Route based on media or command
	if parsed.ImageMessage != nil {
		r.handleImageProof(ctx, parsed)
		return
	}

	r.handleCommand(ctx, parsed)
}

func (r *Router) handleCommand(ctx context.Context, p *ParsedMessage) {
	switch p.Command {
	case CmdBayar:
		r.handleBayar(ctx, p)
	case CmdTagihan:
		r.handleTagihan(ctx, p)
	case CmdStatus:
		r.handleStatus(ctx, p)
	case CmdRiwayat:
		r.handleRiwayat(ctx, p)
	case CmdMenu:
		r.handleMenu(ctx, p)
	case CmdSupport:
		r.handleStartSupport(ctx, p)
	case CmdCloseSupport:
		r.handleCloseSupport(ctx, p)
	case CmdAlreadyTransferred:
		_ = r.waClient.SendText(ctx, p.SenderJID, TemplateWaitingProof())
	case CmdAdminPayments:
		r.handleAdminPayments(ctx, p)
	case CmdAdminInvoice:
		r.handleAdminInvoice(ctx, p)
	case CmdAdminApprove:
		r.handleAdminApprove(ctx, p)
	case CmdAdminReject:
		r.handleAdminReject(ctx, p)
	case CmdAdminMembers:
		r.handleAdminMembers(ctx, p)
	case CmdAdminAddMember:
		r.handleAdminAddMember(ctx, p)
	case CmdAdminDelMember:
		r.handleAdminDelMember(ctx, p)
	case CmdAdminSetExpiry:
		r.handleAdminSetExpiry(ctx, p)
	case CmdAdminReply:
		r.handleAdminReply(ctx, p)
	case CmdAdminCloseSupport:
		r.handleAdminCloseSupport(ctx, p)
	case CmdAdminMenu:
		r.handleAdminMenu(ctx, p)
	case CmdAdminActivateMember:
		r.handleAdminActivateMember(ctx, p)
	case CmdPiket:
		r.handlePiketStatus(ctx, p)
	case CmdListPiket:
		r.handlePiketList(ctx, p)
	case CmdTambahPiket:
		r.handlePiketTambah(ctx, p)
	case CmdGantiPiket:
		r.handlePiketGanti(ctx, p)
	case CmdHapusPiket:
		r.handlePiketHapus(ctx, p)
	case CmdSudahPakan:
		r.handlePiketSudah(ctx, p)
	case CmdResetPiket:
		r.handlePiketReset(ctx, p)
	default:
		// Ignore unrelated chat messages to avoid spamming the user
	}
}

func (r *Router) handleBayar(ctx context.Context, p *ParsedMessage) {
	inv, _, _, isNew, err := r.billingService.GetOrCreateActiveInvoice(ctx, p.SenderJID, p.SenderPhone, p.PushName)
	if err != nil {
		log.Printf("[Router] Failed to create/get invoice: %v", err)
		_ = r.waClient.SendText(ctx, p.SenderJID, "Maaf, terjadi kesalahan saat memproses tagihan kamu. Silakan coba lagi nanti.")
		return
	}

	inst, err := r.billingService.GetPaymentInstructions(ctx, inv.Amount)
	if err != nil {
		_ = r.waClient.SendText(ctx, p.SenderJID, "Gagal memuat instruksi transfer.")
		return
	}

	body := TemplateInvoice(inv, inst, r.cfg.AppTimezone)
	if !isNew {
		body = "⚠️ *Kamu sudah memiliki invoice aktif yang belum dibayar:*\n\n" + body
	}

	buttons := []ButtonOption{
		{ID: "btn_transfer", Text: "📤 Saya Sudah Transfer"},
		{ID: "btn_status", Text: "📊 Cek Status"},
	}

	_ = r.waClient.SendButtons(ctx, p.SenderJID, body, buttons)
}

func (r *Router) handleTagihan(ctx context.Context, p *ParsedMessage) {
	inv, plan, err := r.billingService.GetLatestInvoice(ctx, p.SenderJID, p.SenderPhone)
	if err != nil || inv == nil {
		_ = r.waClient.SendText(ctx, p.SenderJID, "Kamu belum memiliki tagihan aktif. Ketik *bayar* untuk membuat tagihan baru.")
		return
	}

	now := time.Now().UTC()
	if inv.IsActive(now) {
		inst, _ := r.billingService.GetPaymentInstructions(ctx, inv.Amount)
		body := "🧾 *Invoice Aktif Kamu:*\n\n" + TemplateInvoice(inv, inst, r.cfg.AppTimezone)
		buttons := []ButtonOption{
			{ID: "btn_transfer", Text: "📤 Saya Sudah Transfer"},
		}
		_ = r.waClient.SendButtons(ctx, p.SenderJID, body, buttons)
	} else if inv.Status == domain.InvoicePendingReview {
		_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("Invoice *#%s* kamu sedang dalam peninjauan admin ⏳", inv.InvoiceNumber))
	} else {
		_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("Tagihan terakhir kamu (#%s) berstatus *%s*. Ketik *bayar* untuk membuat tagihan baru.", inv.InvoiceNumber, inv.Status))
	}
	_ = plan
}

func (r *Router) handleStatus(ctx context.Context, p *ParsedMessage) {
	sub, plan, subscription, err := r.billingService.GetCustomerStatus(ctx, p.SenderJID, p.SenderPhone)
	if err != nil || sub == nil {
		_ = r.waClient.SendText(ctx, p.SenderJID, "Kamu belum terdaftar sebagai member. Ketik *bayar* untuk mulai mendaftar dan berlangganan.")
		return
	}

	msg := TemplateCustomerStatus(sub, plan, subscription, r.cfg.AppTimezone)
	buttons := []ButtonOption{
		{ID: "btn_bayar", Text: "💳 Bayar Sekarang"},
		{ID: "btn_riwayat", Text: "📜 Riwayat"},
	}
	_ = r.waClient.SendButtons(ctx, p.SenderJID, msg, buttons)
}

func (r *Router) handleRiwayat(ctx context.Context, p *ParsedMessage) {
	invoices, err := r.billingService.GetCustomerHistory(ctx, p.SenderJID, p.SenderPhone, 5)
	if err != nil {
		_ = r.waClient.SendText(ctx, p.SenderJID, "Belum ada riwayat transaksi yang ditemukan.")
		return
	}

	msg := TemplateCustomerHistory(invoices, r.cfg.AppTimezone)
	_ = r.waClient.SendText(ctx, p.SenderJID, msg)
}

func (r *Router) handleMenu(ctx context.Context, p *ParsedMessage) {
	if r.isLeleGroup(p) {
		r.handlePiketMenu(ctx, p)
		return
	}
	isAdmin := r.isAdminSender(p)
	msg := TemplateMenu(isAdmin)
	buttons := []ButtonOption{
		{ID: "btn_bayar", Text: "💳 Bayar Sekarang"},
		{ID: "btn_status", Text: "📊 Cek Status"},
		{ID: "btn_bantuan", Text: "💬 Hubungi Admin"},
	}
	_ = r.waClient.SendButtons(ctx, p.SenderJID, msg, buttons)
}

func (r *Router) handleHelp(ctx context.Context, p *ParsedMessage) {
	r.handleMenu(ctx, p)
}

func (r *Router) handleImageProof(ctx context.Context, p *ParsedMessage) {
	imgBytes, err := r.waClient.DownloadImage(ctx, p.ImageMessage)
	if err != nil {
		log.Printf("[Router] Failed to download proof image: %v", err)
		_ = r.waClient.SendText(ctx, p.SenderJID, "Gagal mengunduh gambar bukti pembayaran. Silakan kirim ulang gambarnya.")
		return
	}

	inv, proof, err := r.billingService.SubmitPaymentProof(ctx, p.SenderJID, p.SenderPhone, imgBytes)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvoiceNotFound):
			_ = r.waClient.SendText(ctx, p.SenderJID, "Tidak ada invoice aktif yang bisa menerima bukti pembayaran.\n\nKetik *bayar* untuk membuat invoice baru.")
		case errors.Is(err, domain.ErrInvoiceExpired):
			_ = r.waClient.SendText(ctx, p.SenderJID, "Invoice kamu sudah kedaluwarsa.\n\nSilakan buat invoice baru dengan mengetik *bayar*.")
		case errors.Is(err, domain.ErrProofAlreadySubmitted):
			_ = r.waClient.SendText(ctx, p.SenderJID, "Bukti transfer untuk invoice ini sudah diterima dan sedang menunggu verifikasi admin ya 🙏")
		case errors.Is(err, domain.ErrFileTooLarge):
			_ = r.waClient.SendText(ctx, p.SenderJID, "Ukuran file bukti pembayaran terlalu besar (maksimal 10MB).")
		case errors.Is(err, domain.ErrInvalidProofMedia):
			_ = r.waClient.SendText(ctx, p.SenderJID, "Format file tidak valid. Mohon kirim berupa foto / gambar (JPG/PNG).")
		default:
			log.Printf("[Router] Submit payment proof error: %v", err)
			_ = r.waClient.SendText(ctx, p.SenderJID, "Terjadi kesalahan saat memproses bukti transfer. Silakan hubungi admin.")
		}
		return
	}

	// Confirm to customer
	_ = r.waClient.SendText(ctx, p.SenderJID, TemplateProofReceived(inv.InvoiceNumber))

	// Notify configured admins
	r.notifyAdminsNewProof(ctx, p.PushName, p.SenderPhone, inv.InvoiceNumber, inv.Amount, proof.FilePath)
}

func (r *Router) notifyAdminsNewProof(ctx context.Context, name, phone, invNumber string, amount int64, filePath string) {
	adminNotice := TemplateAdminNewProof(name, phone, invNumber, amount)

	proofBytes, err := os.ReadFile(filePath)
	hasProofBytes := (err == nil && len(proofBytes) > 0)

	for _, adminJID := range r.cfg.AdminJIDs {
		if hasProofBytes {
			_ = r.waClient.SendImage(ctx, adminJID, proofBytes, adminNotice, "image/jpeg")
		} else {
			_ = r.waClient.SendText(ctx, adminJID, adminNotice)
		}
	}
}

// Admin Commands

func (r *Router) isAdminSender(p *ParsedMessage) bool {
	isAdmin := r.cfg.IsAdmin(p.SenderJID, p.SenderAltJID, p.SenderPhone)
	if !isAdmin {
		log.Printf("[Admin Auth] Access DENIED for Sender=%s (Alt=%s, Chat=%s, Phone=%s, PushName=%q). Configured Admins: %v",
			p.SenderJID, p.SenderAltJID, p.ChatJID, p.SenderPhone, p.PushName, r.cfg.AdminJIDs)
	} else {
		log.Printf("[Admin Auth] Access GRANTED for Sender=%s (Phone=%s)", p.SenderJID, p.SenderPhone)
	}
	return isAdmin
}

func (r *Router) handleAdminPayments(ctx context.Context, p *ParsedMessage) {
	if !r.isAdminSender(p) {
		_ = r.waClient.SendText(ctx, p.SenderJID, "⛔ Perintah ini hanya dapat dijalankan oleh admin terdaftar.")
		return
	}

	invoices, err := r.billingService.GetPendingReviewInvoices(ctx)
	if err != nil {
		_ = r.waClient.SendText(ctx, p.SenderJID, "Gagal memuat daftar pembayaran pending.")
		return
	}

	msg := TemplateAdminPendingList(invoices)
	_ = r.waClient.SendText(ctx, p.SenderJID, msg)
}

func (r *Router) handleAdminInvoice(ctx context.Context, p *ParsedMessage) {
	if !r.isAdminSender(p) {
		_ = r.waClient.SendText(ctx, p.SenderJID, "⛔ Perintah ini hanya dapat dijalankan oleh admin terdaftar.")
		return
	}

	if len(p.CommandArgs) == 0 {
		_ = r.waClient.SendText(ctx, p.SenderJID, "Format salah. Gunakan: `/invoice <NO_INVOICE>`")
		return
	}

	invNumber := strings.ToUpper(p.CommandArgs[0])
	inv, sub, plan, err := r.billingService.GetInvoiceByNumber(ctx, invNumber)
	if err != nil || inv == nil {
		_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("Invoice #%s tidak ditemukan.", invNumber))
		return
	}

	subName := "Member"
	subPhone := "-"
	if sub != nil {
		subName = sub.Name
		subPhone = sub.PhoneNumber
	}
	planName := "Shared Plan"
	if plan != nil {
		planName = plan.Name
	}

	proof, _ := r.billingService.GetPaymentProof(ctx, inv.ID)
	hasProof := "Tidak ada"
	if proof != nil {
		hasProof = fmt.Sprintf("Ada (%s)", proof.MimeType)
	}

	details := fmt.Sprintf(
`🧾 *Detail Invoice #%s*

Member: *%s* (%s)
Plan: *%s*
Nominal: *%s*
Periode: *%s*
Status: *%s*
Expired: *%s*
Bukti Pembayaran: *%s*`,
		inv.InvoiceNumber, subName, subPhone, planName,
		FormatRupiah(inv.Amount), inv.BillingPeriod, inv.Status,
		FormatDate(inv.ExpiresAt, r.cfg.AppTimezone),
		hasProof,
	)

	if proof != nil {
		imgBytes, err := os.ReadFile(proof.FilePath)
		if err == nil && len(imgBytes) > 0 {
			_ = r.waClient.SendImage(ctx, p.SenderJID, imgBytes, details, proof.MimeType)
			return
		}
	}

	_ = r.waClient.SendText(ctx, p.SenderJID, details)
}

func (r *Router) handleAdminApprove(ctx context.Context, p *ParsedMessage) {
	if !r.isAdminSender(p) {
		_ = r.waClient.SendText(ctx, p.SenderJID, "⛔ Perintah ini hanya dapat dijalankan oleh admin terdaftar.")
		return
	}

	if len(p.CommandArgs) == 0 {
		_ = r.waClient.SendText(ctx, p.SenderJID, "Format salah. Gunakan: `/approve <NO_INVOICE>`")
		return
	}

	invNumber := strings.ToUpper(p.CommandArgs[0])
	inv, sub, subscriber, plan, err := r.billingService.ApprovePayment(ctx, invNumber, p.SenderJID)
	if err != nil {
		_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("Gagal approve invoice #%s: %v", invNumber, err))
		return
	}

	planName := "Shared Plan"
	if plan != nil {
		planName = plan.Name
	}

	// Notify subscriber
	if subscriber != nil {
		custMsg := TemplatePaymentSuccess(inv, planName, sub.ExpiresAt, r.cfg.AppTimezone)
		_ = r.waClient.SendText(ctx, subscriber.WhatsAppJID, custMsg)
	}

	// Reply to admin
	adminReply := fmt.Sprintf("✅ Invoice *#%s* berhasil diapprove! Masa aktif langganan telah diperpanjang hingga *%s*.", inv.InvoiceNumber, FormatDate(sub.ExpiresAt, r.cfg.AppTimezone))
	_ = r.waClient.SendText(ctx, p.SenderJID, adminReply)
}

func (r *Router) handleAdminReject(ctx context.Context, p *ParsedMessage) {
	if !r.isAdminSender(p) {
		_ = r.waClient.SendText(ctx, p.SenderJID, "⛔ Perintah ini hanya dapat dijalankan oleh admin terdaftar.")
		return
	}

	if len(p.CommandArgs) == 0 {
		_ = r.waClient.SendText(ctx, p.SenderJID, "Format salah. Gunakan: `/reject <NO_INVOICE> <alasan>`")
		return
	}

	invNumber := strings.ToUpper(p.CommandArgs[0])
	reason := "Bukti transfer tidak sesuai / tidak jelas"
	if len(p.CommandArgs) > 1 {
		reason = strings.Join(p.CommandArgs[1:], " ")
	}

	inv, subscriber, err := r.billingService.RejectPayment(ctx, invNumber, p.SenderJID, reason)
	if err != nil {
		_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("Gagal tolak invoice #%s: %v", invNumber, err))
		return
	}

	// Notify subscriber
	if subscriber != nil {
		custMsg := TemplatePaymentRejected(inv.InvoiceNumber, reason)
		_ = r.waClient.SendText(ctx, subscriber.WhatsAppJID, custMsg)
	}

	// Reply to admin
	adminReply := fmt.Sprintf("❌ Invoice *#%s* berhasil ditolak. Alasan: %s", inv.InvoiceNumber, reason)
	_ = r.waClient.SendText(ctx, p.SenderJID, adminReply)
}

func (r *Router) handleAdminMembers(ctx context.Context, p *ParsedMessage) {
	if !r.isAdminSender(p) {
		_ = r.waClient.SendText(ctx, p.SenderJID, "⛔ Perintah ini hanya dapat dijalankan oleh admin terdaftar.")
		return
	}

	filter := "active"
	if len(p.CommandArgs) > 0 {
		arg := strings.ToLower(p.CommandArgs[0])
		if arg == "inactive" || arg == "nonaktif" {
			filter = "inactive"
		} else if arg == "all" || arg == "semua" {
			filter = "all"
		}
	}

	members, err := r.billingService.ListMembers(ctx)
	if err != nil {
		_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("Gagal memuat daftar member: %v", err))
		return
	}

	msg, buttons := TemplateAdminMemberList(members, filter, r.cfg.AppTimezone)
	_ = r.waClient.SendButtons(ctx, p.SenderJID, msg, buttons)
}

func (r *Router) handleAdminAddMember(ctx context.Context, p *ParsedMessage) {
	if !r.isAdminSender(p) {
		_ = r.waClient.SendText(ctx, p.SenderJID, "⛔ Perintah ini hanya dapat dijalankan oleh admin terdaftar.")
		return
	}

	if len(p.CommandArgs) < 2 {
		_ = r.waClient.SendText(ctx, p.SenderJID, "Format salah. Gunakan:\n`/addmember <nomor> <nama> [paket] [tgl_siklus]`\n\nContoh:\n`/addmember 08123456789 Budi GoogleOne 1`")
		return
	}

	phone := p.CommandArgs[0]
	name := p.CommandArgs[1]
	planName := ""
	cycleDay := 1
	if len(p.CommandArgs) >= 3 {
		planName = p.CommandArgs[2]
	}
	if len(p.CommandArgs) >= 4 {
		fmt.Sscanf(p.CommandArgs[3], "%d", &cycleDay)
	}

	sub, subsc, err := r.billingService.AddMember(ctx, phone, name, planName, cycleDay)
	if err != nil {
		_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("Gagal menambah member: %v", err))
		return
	}

	reply := fmt.Sprintf(
		"✅ *Member Berhasil Ditambahkan!*\n\nNama: *%s*\nNomor: *%s*\nStatus: *ACTIVE*\nJatuh Tempo: *%s*\nSiklus: Tanggal %d setiap bulan",
		sub.Name, FormatPhoneNumber(sub.PhoneNumber), FormatDate(subsc.ExpiresAt, r.cfg.AppTimezone), cycleDay,
	)
	_ = r.waClient.SendText(ctx, p.SenderJID, reply)
}

func (r *Router) handleAdminDelMember(ctx context.Context, p *ParsedMessage) {
	if !r.isAdminSender(p) {
		_ = r.waClient.SendText(ctx, p.SenderJID, "⛔ Perintah ini hanya dapat dijalankan oleh admin terdaftar.")
		return
	}

	if len(p.CommandArgs) == 0 {
		_ = r.waClient.SendText(ctx, p.SenderJID, "Format salah. Gunakan:\n`/delmember <nomor>`\n\nContoh:\n`/delmember 08123456789`")
		return
	}

	phone := p.CommandArgs[0]
	err := r.billingService.DeactivateMember(ctx, phone)
	if err != nil {
		_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("Gagal menonaktifkan member: %v", err))
		return
	}

	formattedPhone := FormatPhoneNumber(phone)
	_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("✅ Member dengan nomor *%s* berhasil dinonaktifkan.\n\nMember ini tidak akan menerima reminder tagihan bulanan.\nUntuk mengaktifkan kembali di masa mendatang:\n`/aktifkan %s`", formattedPhone, formattedPhone))
}

func (r *Router) handleAdminActivateMember(ctx context.Context, p *ParsedMessage) {
	if !r.isAdminSender(p) {
		_ = r.waClient.SendText(ctx, p.SenderJID, "⛔ Perintah ini hanya dapat dijalankan oleh admin terdaftar.")
		return
	}

	if len(p.CommandArgs) == 0 {
		_ = r.waClient.SendText(ctx, p.SenderJID, "Format salah. Gunakan:\n`/aktifkan <nomor>`\n\nContoh:\n`/aktifkan 08123456789`")
		return
	}

	phone := p.CommandArgs[0]
	err := r.billingService.ActivateMember(ctx, phone)
	if err != nil {
		_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("Gagal mengaktifkan member: %v", err))
		return
	}

	_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("✅ Member dengan nomor *%s* berhasil diaktifkan kembali! 🟢", FormatPhoneNumber(phone)))
}

func (r *Router) handleAdminSetExpiry(ctx context.Context, p *ParsedMessage) {
	if !r.isAdminSender(p) {
		_ = r.waClient.SendText(ctx, p.SenderJID, "⛔ Perintah ini hanya dapat dijalankan oleh admin terdaftar.")
		return
	}

	if len(p.CommandArgs) < 2 {
		_ = r.waClient.SendText(ctx, p.SenderJID, "Format salah. Gunakan:\n`/setexpiry <nomor> <YYYY-MM-DD>`\n\nContoh:\n`/setexpiry 08123456789 2026-10-01`")
		return
	}

	phone := p.CommandArgs[0]
	dateStr := p.CommandArgs[1]

	loc := r.cfg.AppTimezone
	if loc == nil {
		loc = time.UTC
	}
	t, err := time.ParseInLocation("2006-01-02", dateStr, loc)
	if err != nil {
		_ = r.waClient.SendText(ctx, p.SenderJID, "Format tanggal salah. Gunakan format YYYY-MM-DD, contoh: 2026-10-01")
		return
	}

	expiry := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, loc).UTC()
	err = r.billingService.SetMemberExpiry(ctx, phone, expiry)
	if err != nil {
		_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("Gagal mengatur masa aktif: %v", err))
		return
	}

	_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("✅ Masa aktif nomor *%s* berhasil diatur sampai *%s*.", phone, FormatDate(expiry, r.cfg.AppTimezone)))
}

// Live Support Handlers

func (r *Router) handleStartSupport(ctx context.Context, p *ParsedMessage) {
	if r.isLeleGroup(p) {
		r.handlePiketMenu(ctx, p)
		return
	}

	phone := p.SenderPhone
	if phone == "" {
		phone = p.SenderJID
	}
	r.supportMgr.OpenSession(phone, p.SenderJID, p.PushName)

	name := p.PushName
	if sub, _ := r.billingService.IsRegisteredSubscriber(ctx, p.SenderJID, p.SenderPhone); sub != nil && sub.Name != "" {
		name = sub.Name
	}

	welcomeMsg := TemplateSupportWelcome(name)
	buttons := []ButtonOption{
		{ID: "btn_selesai", Text: "✅ Selesai"},
		{ID: "btn_menu", Text: "📋 Menu Utama"},
	}
	_ = r.waClient.SendButtons(ctx, p.SenderJID, welcomeMsg, buttons)

	// Notify admin that member opened a support session
	adminNotice := fmt.Sprintf("💬 *Sesi Live Support Dimulai*\n\nMember: *%s* (%s)\nStatus: Sesi aktif.\nPesan yang dikirim member akan langsung diteruskan ke chat ini.", name, phone)
	for _, adminJID := range r.cfg.AdminJIDs {
		_ = r.waClient.SendText(ctx, adminJID, adminNotice)
	}
}

func (r *Router) handleCustomerSupportMessage(ctx context.Context, p *ParsedMessage) {
	inquiry := strings.TrimSpace(p.RawText)
	if inquiry == "" && p.ImageMessage == nil {
		log.Printf("[Router] Ignored empty support message from %s", p.SenderJID)
		return
	}

	name := p.PushName
	phone := p.SenderPhone
	if sub, _ := r.billingService.IsRegisteredSubscriber(ctx, p.SenderJID, p.SenderPhone); sub != nil {
		if sub.Name != "" {
			name = sub.Name
		}
		if sub.PhoneNumber != "" {
			phone = sub.PhoneNumber
		}
	}

	if inquiry == "" && p.ImageMessage != nil {
		inquiry = "[Mengirim Foto / Tangkapan Layar]"
	}

	adminNotice := TemplateAdminSupportForward(name, phone, inquiry)

	var imgBytes []byte
	if p.ImageMessage != nil {
		imgBytes, _ = r.waClient.DownloadImage(ctx, p.ImageMessage)
	}

	for _, adminJID := range r.cfg.AdminJIDs {
		if len(imgBytes) > 0 {
			_ = r.waClient.SendImage(ctx, adminJID, imgBytes, adminNotice, "image/jpeg")
		} else {
			_ = r.waClient.SendText(ctx, adminJID, adminNotice)
		}
	}

	// Feedback to customer (throttled to avoid duplicate spam on rapid/consecutive messages)
	userKey := p.SenderPhone
	if userKey == "" {
		userKey = p.SenderJID
	}
	if r.supportMgr.ShouldSendFeedback(userKey) {
		_ = r.waClient.SendText(ctx, p.SenderJID, "📨 Pesan kamu telah diteruskan ke Admin. Mohon tunggu balasan ya 🙏\n\n_(Ketik *selesai* jika kendala sudah terselesaikan)_")
	}
}

func (r *Router) handleCloseSupport(ctx context.Context, p *ParsedMessage) {
	phone := p.SenderPhone
	if phone == "" {
		phone = p.SenderJID
	}
	r.supportMgr.CloseSession(phone)

	name := p.PushName
	if sub, _ := r.billingService.IsRegisteredSubscriber(ctx, p.SenderJID, p.SenderPhone); sub != nil && sub.Name != "" {
		name = sub.Name
	}

	_ = r.waClient.SendText(ctx, p.SenderJID, TemplateSupportClosedCustomer())

	// Notify admins
	adminNotice := TemplateSupportClosedAdmin(name, phone)
	for _, adminJID := range r.cfg.AdminJIDs {
		_ = r.waClient.SendText(ctx, adminJID, adminNotice)
	}
}

func (r *Router) handleAdminReply(ctx context.Context, p *ParsedMessage) {
	if !r.isAdminSender(p) {
		_ = r.waClient.SendText(ctx, p.SenderJID, "⛔ Perintah ini hanya dapat dijalankan oleh admin terdaftar.")
		return
	}

	if len(p.CommandArgs) < 2 {
		_ = r.waClient.SendText(ctx, p.SenderJID, "Format salah. Gunakan:\n`/reply <nomor> <pesan>`\n\nContoh:\n`/reply 08123456789 Halo kak, ada yang bisa dibantu?`")
		return
	}

	targetPhone := config.NormalizePhone(p.CommandArgs[0])
	if targetPhone == "" {
		_ = r.waClient.SendText(ctx, p.SenderJID, "Nomor tujuan tidak valid.")
		return
	}

	replyText := strings.Join(p.CommandArgs[1:], " ")

	// Find destination JID: check active session first, then subscriber DB, then fallback to phone@s.whatsapp.net
	destJID := ""
	if session := r.supportMgr.GetSession(targetPhone); session != nil && session.CustomerJID != "" {
		destJID = session.CustomerJID
	} else if sub, _ := r.billingService.IsRegisteredSubscriber(ctx, "", targetPhone); sub != nil && sub.WhatsAppJID != "" {
		destJID = sub.WhatsAppJID
	} else {
		destJID = targetPhone + "@s.whatsapp.net"
	}

	customerMsg := TemplateCustomerAdminReply(replyText)
	err := r.waClient.SendText(ctx, destJID, customerMsg)
	if err != nil {
		log.Printf("[Router] Failed to send admin reply to %s (%s): %v", targetPhone, destJID, err)
		_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("❌ Gagal mengirim balasan ke %s: %v", targetPhone, err))
		return
	}

	_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("✅ Balasan berhasil dikirim ke *%s*.", targetPhone))
}

func (r *Router) handleAdminCloseSupport(ctx context.Context, p *ParsedMessage) {
	if !r.isAdminSender(p) {
		_ = r.waClient.SendText(ctx, p.SenderJID, "⛔ Perintah ini hanya dapat dijalankan oleh admin terdaftar.")
		return
	}

	if len(p.CommandArgs) < 1 {
		_ = r.waClient.SendText(ctx, p.SenderJID, "Format salah. Gunakan:\n`/closesession <nomor>`\n\nContoh:\n`/closesession 08123456789`")
		return
	}

	targetPhone := config.NormalizePhone(p.CommandArgs[0])
	r.supportMgr.CloseSession(targetPhone)

	destJID := targetPhone + "@s.whatsapp.net"
	if sub, _ := r.billingService.IsRegisteredSubscriber(ctx, "", targetPhone); sub != nil && sub.WhatsAppJID != "" {
		destJID = sub.WhatsAppJID
	}
	_ = r.waClient.SendText(ctx, destJID, TemplateSupportClosedCustomer())

	_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("✅ Sesi bantuan untuk *%s* berhasil ditutup.", targetPhone))
}

func (r *Router) handleAdminMenu(ctx context.Context, p *ParsedMessage) {
	if !r.isAdminSender(p) {
		_ = r.waClient.SendText(ctx, p.SenderJID, "⛔ Perintah ini hanya dapat dijalankan oleh admin terdaftar.")
		return
	}
	msg := TemplateAdminMenu()
	_ = r.waClient.SendText(ctx, p.SenderJID, msg)
}

// isExplicitBotCommand checks if a message from a customer in a support session is an intentional bot command attempt.
func isExplicitBotCommand(p *ParsedMessage) bool {
	if p.ButtonID != "" && p.ButtonID != "btn_selesai" {
		return true
	}
	raw := strings.ToLower(strings.TrimSpace(p.RawText))
	if strings.HasPrefix(raw, "/") {
		return true
	}
	switch raw {
	case "menu", "bayar", "tagihan", "status", "riwayat", "bantuan", "help", "invoice", "langganan", "history", "1", "2", "3", "4", "5":
		return true
	}
	return false
}

// isPiketCommand checks if the command is a piket lele related command.
func isPiketCommand(cmd CommandType) bool {
	switch cmd {
	case CmdPiket, CmdTambahPiket, CmdHapusPiket, CmdGantiPiket, CmdListPiket, CmdSudahPakan, CmdResetPiket, CmdPiketMenu:
		return true
	}
	return false
}

// Piket Lele (Catfish Feeding Roster) Handlers

func (r *Router) isLeleGroup(p *ParsedMessage) bool {
	if r.cfg.LeleGroupJID == "" || !p.IsGroup {
		return false
	}
	cleanGroupConfig := config.NormalizePhone(r.cfg.LeleGroupJID)
	cleanChat := config.NormalizePhone(p.ChatJID)
	if cleanGroupConfig != "" && cleanGroupConfig == cleanChat {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(p.ChatJID), strings.TrimSpace(r.cfg.LeleGroupJID))
}

func (r *Router) handleLeleGroupMessage(ctx context.Context, p *ParsedMessage) {
	if r.piketService == nil {
		return
	}

	// 1. Photo handling with anti-spam
	if p.ImageMessage != nil {
		r.handlePiketPhoto(ctx, p)
		return
	}

	// 2. Command handling
	switch p.Command {
	case CmdSudahPakan:
		r.handlePiketSudah(ctx, p)
	case CmdPiket:
		r.handlePiketStatus(ctx, p)
	case CmdTambahPiket:
		r.handlePiketTambah(ctx, p)
	case CmdHapusPiket:
		r.handlePiketHapus(ctx, p)
	case CmdGantiPiket:
		r.handlePiketGanti(ctx, p)
	case CmdListPiket:
		r.handlePiketList(ctx, p)
	case CmdResetPiket:
		r.handlePiketReset(ctx, p)
	case CmdMenu, CmdSupport, CmdPiketMenu:
		r.handlePiketMenu(ctx, p)
	default:
		// Normal casual conversations/memes in the group are SILENTLY IGNORED!
	}
}

func (r *Router) handlePiketPhoto(ctx context.Context, p *ParsedMessage) {
	imgBytes, err := r.waClient.DownloadImage(ctx, p.ImageMessage)
	if err != nil {
		log.Printf("[Piket] Failed to download group photo: %v", err)
		return
	}

	logRecord, slot, err := r.piketService.SubmitPhotoProof(ctx, p.SenderJID, p.SenderPhone, p.RawText, imgBytes, time.Now())
	if err != nil {
		if errors.Is(err, piket.ErrPiketAlreadyDone) || errors.Is(err, piket.ErrUnauthorizedFeeder) {
			// Anti-spam: Silently ignore photos sent by other members or when already done
			log.Printf("[Piket Anti-Spam] Ignored photo from %s: %v", p.SenderPhone, err)
			return
		}
		log.Printf("[Piket] Photo proof error: %v", err)
		return
	}

	name := p.PushName
	if name == "" {
		name = p.SenderPhone
	}
	_ = slot

	reply := TemplatePiketSuccess(name, *logRecord.FedAt, r.cfg.AppTimezone)
	_ = r.waClient.SendText(ctx, p.ChatJID, reply)
}

func (r *Router) isTodayFeederOrAdmin(ctx context.Context, p *ParsedMessage) (bool, error) {
	if r.isAdminSender(p) {
		return true, nil
	}

	todayLog, todaySlot, err := r.piketService.GetTodaySlotAndLog(ctx, time.Now())
	if err != nil {
		return false, err
	}

	senderPhone := p.SenderPhone
	if senderPhone == "" {
		senderPhone = config.NormalizePhone(p.SenderJID)
	}
	cleanPhone := config.NormalizePhone(senderPhone)

	// 1. Check against today's assigned slot members
	if todaySlot != nil {
		for _, m := range todaySlot.Members {
			cleanMPhone := config.NormalizePhone(m.PhoneNumber)
			if cleanPhone != "" && cleanMPhone != "" && cleanMPhone == cleanPhone {
				return true, nil
			}
			cleanMJID := config.NormalizePhone(m.WhatsAppJID)
			if cleanPhone != "" && cleanMJID != "" && cleanMJID == cleanPhone {
				return true, nil
			}
			if p.SenderJID != "" && (m.WhatsAppJID == p.SenderJID || m.PhoneNumber == p.SenderJID) {
				return true, nil
			}
			if p.SenderAltJID != "" && (m.WhatsAppJID == p.SenderAltJID || m.PhoneNumber == p.SenderAltJID) {
				return true, nil
			}
			if strings.Contains(p.SenderJID, "@lid") {
				lidUser := strings.Split(p.SenderJID, "@")[0]
				if strings.Contains(m.WhatsAppJID, lidUser) || strings.Contains(m.PhoneNumber, lidUser) {
					return true, nil
				}
			}
		}
	}

	// 2. Check if today's log has an overridden display name that matches sender
	if todayLog != nil && todayLog.AssignedMembersDisplay != "" {
		disp := todayLog.AssignedMembersDisplay
		if cleanPhone != "" && strings.Contains(disp, cleanPhone) {
			return true, nil
		}
		if p.SenderPhone != "" && strings.Contains(disp, p.SenderPhone) {
			return true, nil
		}
		if strings.Contains(p.SenderJID, "@lid") {
			lidUser := strings.Split(p.SenderJID, "@")[0]
			if strings.Contains(disp, lidUser) {
				return true, nil
			}
		}
	}

	return false, nil
}

func (r *Router) handlePiketSudah(ctx context.Context, p *ParsedMessage) {
	canConfirm, err := r.isTodayFeederOrAdmin(ctx, p)
	if err != nil && !errors.Is(err, piket.ErrNoSlotsConfigured) {
		log.Printf("[Piket] Error checking authorization: %v", err)
	}
	if !canConfirm {
		_ = r.waClient.SendText(ctx, p.ChatJID, "⛔ Kamu bukan petugas piket hari ini bro. Hanya petugas piket hari ini atau admin yang dapat konfirmasi pakan lele.")
		return
	}

	logRecord, slot, err := r.piketService.MarkDoneManually(ctx, p.SenderJID, p.SenderPhone, time.Now())
	if err != nil {
		if errors.Is(err, piket.ErrPiketAlreadyDone) {
			_ = r.waClient.SendText(ctx, p.ChatJID, "ℹ️ Piket lele hari ini sudah tercatat selesai sebelumnya ya bro 👍")
			return
		}
		if errors.Is(err, piket.ErrNoSlotsConfigured) {
			_ = r.waClient.SendText(ctx, p.ChatJID, "⚠️ Belum ada jadwal piket yang terdaftar. Ketik `/tambahpiket @User` untuk mendaftarkan jadwal.")
			return
		}
		_ = r.waClient.SendText(ctx, p.ChatJID, fmt.Sprintf("Gagal konfirmasi piket: %v", err))
		return
	}
	_ = slot
	name := p.PushName
	if name == "" {
		name = p.SenderPhone
	}
	reply := TemplatePiketSuccess(name, *logRecord.FedAt, r.cfg.AppTimezone)
	_ = r.waClient.SendText(ctx, p.ChatJID, reply)
}

func getMemberMentionTag(m piket.Member) (tag string, jid string) {
	jid = m.WhatsAppJID
	user := m.PhoneNumber
	if jid != "" {
		if parts := strings.Split(jid, "@"); len(parts) > 0 && parts[0] != "" {
			user = parts[0]
		}
	}
	if user == "" {
		user = m.Name
	}
	return "@" + user, jid
}

func (r *Router) handlePiketStatus(ctx context.Context, p *ParsedMessage) {
	schedule, err := r.piketService.GetWeeklySchedule(ctx, time.Now())
	if err != nil {
		if errors.Is(err, piket.ErrNoSlotsConfigured) {
			_ = r.waClient.SendText(ctx, p.ChatJID, "⚠️ Belum ada anggota piket yang terdaftar.\n\nContoh tambah piket:\n`/tambahpiket @Budi` (Solo)\n`/tambahpiket @Budi @Fahad` (Duet 🛵)")
			return
		}
		_ = r.waClient.SendText(ctx, p.ChatJID, fmt.Sprintf("Gagal memuat jadwal piket: %v", err))
		return
	}

	todayLog, _, _ := r.piketService.GetTodaySlotAndLog(ctx, time.Now())
	statusStr := "⏳ *Belum dikasih pakan*"
	if todayLog != nil && todayLog.Status == piket.StatusDone {
		statusStr = "✅ *Sudah dikasih pakan (Selesai)*"
	}

	var sb strings.Builder
	sb.WriteString("🐟 *JADWAL PIKET PAKAN LELE*\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString(fmt.Sprintf("Status Hari Ini: %s\n", statusStr))
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━\n\n")
	sb.WriteString("📅 *Roster 7 Hari ke Depan:*\n\n")

	var mentionJIDs []string
	seenJID := make(map[string]bool)

	for i, day := range schedule {
		var memberNames []string
		for _, m := range day.Members {
			tag, jid := getMemberMentionTag(m)
			memberNames = append(memberNames, tag)
			if jid != "" && !seenJID[jid] {
				seenJID[jid] = true
				mentionJIDs = append(mentionJIDs, jid)
			}
		}
		namesStr := strings.Join(memberNames, " & ")
		slotType := "Solo"
		if len(day.Members) > 1 {
			slotType = "Duet 🛵"
		}

		if day.IsToday {
			sb.WriteString(fmt.Sprintf("👉 *%d. %s (%s) — HARI INI*\n   👥 Petugas: %s (%s)\n\n", i+1, day.DayName, day.Date, namesStr, slotType))
		} else {
			sb.WriteString(fmt.Sprintf("   *%d. %s* (%s)\n   👥 Petugas: %s (%s)\n\n", i+1, day.DayName, day.Date, namesStr, slotType))
		}
	}

	sb.WriteString("━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString("Ketik *sudah* atau kirim foto kolam jika sudah memberi pakan.")

	_ = r.waClient.SendTextWithMentions(ctx, p.ChatJID, strings.TrimSpace(sb.String()), mentionJIDs)
}

func (r *Router) handlePiketTambah(ctx context.Context, p *ParsedMessage) {
	if !r.isAdminSender(p) {
		_ = r.waClient.SendText(ctx, p.ChatJID, "⛔ Hanya admin yang dapat menambah atau mengatur susunan slot piket.")
		return
	}

	seen := make(map[string]bool)
	var members []piket.Member

	// Helper to add sender themselves (for when user cannot tag their own account in WhatsApp)
	senderPhone := p.SenderPhone
	if senderPhone == "" {
		senderPhone = config.NormalizePhone(p.SenderJID)
	}
	senderJID := p.SenderAltJID
	if senderJID == "" {
		senderJID = p.SenderJID
	}
	senderUser := senderPhone
	if senderUser == "" {
		senderUser = senderJID
	}

	addSender := func() {
		if !seen[senderUser] && senderUser != "" {
			seen[senderUser] = true
			name := p.PushName
			if name == "" {
				name = senderPhone
			}
			members = append(members, piket.Member{
				Name:        name,
				PhoneNumber: senderPhone,
				WhatsAppJID: senderJID,
			})
		}
	}

	// 1. If someone just types "/tambahpiket" with NO arguments and NO mentions:
	// Automatically register the sender!
	if len(p.CommandArgs) == 0 && len(p.MentionedJIDs) == 0 {
		addSender()
	}

	// 2. Process tagged members
	if len(p.MentionedJIDs) > 0 {
		for _, jid := range p.MentionedJIDs {
			user := jid
			if atIdx := strings.Index(jid, "@"); atIdx != -1 {
				user = jid[:atIdx]
			}
			if seen[user] {
				continue
			}
			seen[user] = true

			phone := config.NormalizePhone(user)
			if phone == "" {
				phone = user
			}
			members = append(members, piket.Member{
				Name:        phone,
				PhoneNumber: phone,
				WhatsAppJID: jid,
			})
		}
	}

	// 3. Process args: check for self keywords ("saya", "gw", "aku", "me") or phone numbers
	if len(p.CommandArgs) > 0 {
		for _, arg := range p.CommandArgs {
			lower := strings.ToLower(strings.TrimSpace(arg))
			switch lower {
			case "saya", "gw", "gue", "aku", "me", "gua", "diriku", "ane", "self":
				addSender()
			default:
				cleanPhone := config.NormalizePhone(arg)
				if cleanPhone != "" && !seen[cleanPhone] {
					seen[cleanPhone] = true
					members = append(members, piket.Member{
						Name:        cleanPhone,
						PhoneNumber: cleanPhone,
						WhatsAppJID: cleanPhone + "@s.whatsapp.net",
					})
				}
			}
		}
	}

	if len(members) == 0 {
		_ = r.waClient.SendText(ctx, p.ChatJID, "Format salah. Kamu bisa gunakan:\n\n*Daftar Diri Sendiri (Solo):*\n`/tambahpiket saya` atau cukup ketik `/tambahpiket`\n\n*Daftar Orang Lain (Solo):*\n`/tambahpiket @Teman`\n\n*Duet (Barengan Kamu & Teman):*\n`/tambahpiket saya @Teman`\n\n*Duet (2 Teman):*\n`/tambahpiket @Teman1 @Teman2`")
		return
	}

	slotType := "Solo"
	if len(members) > 1 {
		slotType = "Duet 🛵"
	}

	var mentionJIDs []string
	var displayNames []string
	for _, m := range members {
		tag, jid := getMemberMentionTag(m)
		if jid != "" {
			mentionJIDs = append(mentionJIDs, jid)
		}
		displayNames = append(displayNames, tag)
	}

	slotName := strings.Join(displayNames, " & ")
	slot, err := r.piketService.RegisterSlot(ctx, slotName, members)
	if err != nil {
		_ = r.waClient.SendText(ctx, p.ChatJID, fmt.Sprintf("Gagal menambah slot piket: %v", err))
		return
	}

	reply := fmt.Sprintf(
		"✅ *Slot Piket Berhasil Ditambahkan!*\n\n📌 Slot: #%d (%s)\n👥 Petugas: %s\n\nJadwal akan berputar otomatis secara adil.",
		slot.RotationOrder, slotType, slotName,
	)
	_ = r.waClient.SendTextWithMentions(ctx, p.ChatJID, reply, mentionJIDs)
}

func (r *Router) handlePiketHapus(ctx context.Context, p *ParsedMessage) {
	if !r.isAdminSender(p) {
		_ = r.waClient.SendText(ctx, p.ChatJID, "⛔ Hanya admin yang dapat menghapus slot piket.")
		return
	}

	if len(p.CommandArgs) == 0 && len(p.MentionedJIDs) == 0 {
		_ = r.waClient.SendText(ctx, p.ChatJID, "Format: `/hapuspiket @Orang` atau `/hapuspiket <Nomor_Slot>`\nContoh: `/hapuspiket @Ari` atau `/hapuspiket 1`")
		return
	}

	arg := ""
	if len(p.CommandArgs) > 0 {
		arg = p.CommandArgs[0]
	}

	deletedSlot, err := r.piketService.DeleteSlotByQuery(ctx, arg, p.MentionedJIDs)
	if err != nil {
		_ = r.waClient.SendText(ctx, p.ChatJID, fmt.Sprintf("⚠️ %v", err))
		return
	}

	_ = r.waClient.SendText(ctx, p.ChatJID, fmt.Sprintf("✅ Slot piket #%d (%s) berhasil dihapus.", deletedSlot.RotationOrder, deletedSlot.Name))
}

func (r *Router) handlePiketReset(ctx context.Context, p *ParsedMessage) {
	if !r.isAdminSender(p) {
		_ = r.waClient.SendText(ctx, p.ChatJID, "⛔ Hanya admin yang dapat mereset jadwal piket.")
		return
	}

	err := r.piketService.ResetAllSlots(ctx)
	if err != nil {
		_ = r.waClient.SendText(ctx, p.ChatJID, fmt.Sprintf("Gagal reset piket: %v", err))
		return
	}
	_ = r.waClient.SendText(ctx, p.ChatJID, "🗑️ Semua slot piket lele telah berhasil direset / dikosongkan. Silakan daftarkan ulang dengan `/tambahpiket`.")
}

func (r *Router) handlePiketGanti(ctx context.Context, p *ParsedMessage) {
	canGanti, err := r.isTodayFeederOrAdmin(ctx, p)
	if err != nil && !errors.Is(err, piket.ErrNoSlotsConfigured) {
		log.Printf("[Piket] Error checking authorization: %v", err)
	}
	if !canGanti {
		_ = r.waClient.SendText(ctx, p.ChatJID, "⛔ Hanya admin atau petugas piket hari ini yang dapat mengalihkan jadwal piket.")
		return
	}

	seen := make(map[string]bool)
	var mentionJIDs []string
	var names []string

	senderPhone := p.SenderPhone
	if senderPhone == "" {
		senderPhone = config.NormalizePhone(p.SenderJID)
	}
	senderJID := p.SenderAltJID
	if senderJID == "" {
		senderJID = p.SenderJID
	}
	senderUser := senderPhone
	if senderUser == "" {
		senderUser = senderJID
	}

	addSender := func() {
		if !seen[senderUser] && senderUser != "" {
			seen[senderUser] = true
			mentionJIDs = append(mentionJIDs, senderJID)
			names = append(names, "@"+senderPhone)
		}
	}

	if len(p.CommandArgs) == 0 && len(p.MentionedJIDs) == 0 {
		addSender()
	}

	if len(p.MentionedJIDs) > 0 {
		for _, jid := range p.MentionedJIDs {
			user := jid
			if atIdx := strings.Index(jid, "@"); atIdx != -1 {
				user = jid[:atIdx]
			}
			if seen[user] {
				continue
			}
			seen[user] = true

			phone := config.NormalizePhone(user)
			if phone == "" {
				phone = user
			}
			mentionJIDs = append(mentionJIDs, jid)
			names = append(names, "@"+phone)
		}
	}

	if len(p.CommandArgs) > 0 {
		for _, arg := range p.CommandArgs {
			lower := strings.ToLower(strings.TrimSpace(arg))
			switch lower {
			case "saya", "gw", "gue", "aku", "me", "gua", "diriku", "ane", "self":
				addSender()
			default:
				clean := config.NormalizePhone(arg)
				if clean != "" && !seen[clean] {
					seen[clean] = true
					mentionJIDs = append(mentionJIDs, clean+"@s.whatsapp.net")
					names = append(names, "@"+clean)
				}
			}
		}
	}

	if len(names) == 0 {
		_ = r.waClient.SendText(ctx, p.ChatJID, "Format salah. Tag orang yang menggantikan:\nContoh: `/gantipiket @Joko` atau `/gantipiket @Joko @Iwan`")
		return
	}

	display := strings.Join(names, " & ")
	err = r.piketService.SwapTodayPiket(ctx, time.Now(), display)
	if err != nil {
		_ = r.waClient.SendText(ctx, p.ChatJID, fmt.Sprintf("Gagal mengganti piket: %v", err))
		return
	}

	reply := fmt.Sprintf("🔄 *Piket Hari Ini Berhasil Dialihkan!*\n\nPetugas hari ini sekarang: %s\nSiap-siap meluncur jam 16:30 WIB ya bro! 🛵", display)
	_ = r.waClient.SendTextWithMentions(ctx, p.ChatJID, reply, mentionJIDs)
}

func (r *Router) handlePiketList(ctx context.Context, p *ParsedMessage) {
	slots, err := r.piketService.ListSlots(ctx)
	if err != nil || len(slots) == 0 {
		_ = r.waClient.SendText(ctx, p.ChatJID, "⚠️ Belum ada slot piket yang terdaftar. Ketik `/tambahpiket @User`.")
		return
	}

	var sb strings.Builder
	sb.WriteString("📋 *DAFTAR GILIRAN PIKET LELE*\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━\n\n")

	var mentionJIDs []string
	seenJID := make(map[string]bool)

	for _, s := range slots {
		slotType := "Solo"
		if len(s.Members) > 1 {
			slotType = "Duet 🛵"
		}
		var memberNames []string
		for _, m := range s.Members {
			tag, jid := getMemberMentionTag(m)
			memberNames = append(memberNames, tag)
			if jid != "" && !seenJID[jid] {
				seenJID[jid] = true
				mentionJIDs = append(mentionJIDs, jid)
			}
		}

		sb.WriteString(fmt.Sprintf("*Slot #%d* (%s)\n👥 Petugas: %s\n\n", s.RotationOrder, slotType, strings.Join(memberNames, " & ")))
	}

	sb.WriteString("━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString("💡 *Info Giliran:*\n")
	sb.WriteString("• Urutan giliran berputar tiap hari: Slot #1 ➔ Slot #2 ➔ dst.\n")
	sb.WriteString("• Hapus slot: `/hapuspiket <Nomor_Slot>` (contoh: `/hapuspiket 1`)\n")
	sb.WriteString("• Tambah slot: `/tambahpiket @Orang` (atau `/tambahpiket saya`)")

	_ = r.waClient.SendTextWithMentions(ctx, p.ChatJID, strings.TrimSpace(sb.String()), mentionJIDs)
}

func (r *Router) handlePiketMenu(ctx context.Context, p *ParsedMessage) {
	isAdmin := r.isAdminSender(p)
	var sb strings.Builder
	sb.WriteString("🐟 *MENU BOT PIKET LELE* 🐟\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString("Halo! Berikut daftar perintah piket pakan lele di grup ini:\n\n")

	sb.WriteString("📋 *Jadwal & Giliran:*\n")
	sb.WriteString("• `/piket` — Cek petugas hari ini & roster 7 hari\n")
	sb.WriteString("• `/listpiket` — Cek susunan semua slot giliran\n\n")

	sb.WriteString("🛵 *Konfirmasi Pakan:*\n")
	sb.WriteString("• `sudah` / `beres` — Konfirmasi lele sudah diberi pakan\n")
	sb.WriteString("• Kirim Foto Kolam — Foto bukti pakan (hanya petugas hari ini/admin)\n\n")

	sb.WriteString("🔄 *Tukar Giliran:*\n")
	sb.WriteString("• `/gantipiket @Teman` — Alihkan tugas hari ini ke teman\n\n")

	if isAdmin {
		sb.WriteString("⚙️ *Pengaturan Admin:*\n")
		sb.WriteString("• `/tambahpiket` — Daftarkan diri sendiri (Solo)\n")
		sb.WriteString("• `/tambahpiket @Teman` — Daftarkan teman (Solo)\n")
		sb.WriteString("• `/tambahpiket saya @Teman` — Daftarkan slot Duet 🛵\n")
		sb.WriteString("• `/hapuspiket <Nomor>` — Hapus slot piket (contoh: `/hapuspiket 1`)\n")
		sb.WriteString("• `/resetpiket` — Kosongkan & reset semua jadwal piket\n\n")
	}

	sb.WriteString("━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString("⏰ *Jadwal Pengingat Bot:*\n")
	sb.WriteString("• 15:00 WIB — Peringatan awal bersiap\n")
	sb.WriteString("• 16:30 WIB — Waktunya pakan lele\n")
	sb.WriteString("• 17:30 WIB — Alarm darurat jika belum ada yang pakan!")

	_ = r.waClient.SendText(ctx, p.ChatJID, strings.TrimSpace(sb.String()))
}

