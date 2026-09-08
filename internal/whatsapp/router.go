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
	"wabill/internal/storage"
)

type Router struct {
	cfg            *config.Config
	billingService *billing.Service
	waClient       WhatsAppClient
	dedupRepo      *storage.DeduplicationRepo
	limiter        *UserRateLimiter
	userLock       *KeyedMutex
	supportMgr     *SupportSessionManager
}

func NewRouter(
	cfg *config.Config,
	billingSvc *billing.Service,
	waClient WhatsAppClient,
	dedupRepo *storage.DeduplicationRepo,
) *Router {
	return &Router{
		cfg:            cfg,
		billingService: billingSvc,
		waClient:       waClient,
		dedupRepo:      dedupRepo,
		limiter: NewUserRateLimiter(RateLimiterConfig{
			MaxRequests:    5,
			WindowDuration: 10 * time.Second,
			Cooldown:       8 * time.Second,
		}),
		userLock:   NewKeyedMutex(),
		supportMgr: NewSupportSessionManager(1 * time.Hour),
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

	// 3. Customer Whitelist: ONLY respond if sender is an Admin OR an Active Registered Subscriber!
	// Non-registered numbers (friends, family, casual personal chats) are SILENTLY IGNORED.
	isAdmin := r.isAdminSender(parsed)
	if !isAdmin {
		sub, err := r.billingService.IsRegisteredSubscriber(ctx, parsed.SenderJID, parsed.SenderPhone)
		if err != nil || sub == nil {
			log.Printf("[Router] Ignored message from unregistered sender Sender=%s (Phone=%s)", parsed.SenderJID, parsed.SenderPhone)
			return
		}
	}

	// 4. Mark as read immediately for authorized senders (Centang biru)
	_ = r.waClient.MarkRead(ctx, msgEvt.Info.Chat, msgEvt.Info.Sender, []types.MessageID{msgEvt.Info.ID}, msgEvt.Info.Timestamp)

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

		// In an active support session, customer is chatting directly with human admin.
		// Only explicit close or explicit menu button clicks are intercepted.
		// All conversational text, greetings, and questions MUST be forwarded to the admin!
		if parsed.Command == CmdCloseSupport {
			typingMs := 1000 + rand.Intn(500)
			_ = r.waClient.SimulateTyping(ctx, parsed.SenderJID, time.Duration(typingMs)*time.Millisecond)
			r.handleCloseSupport(ctx, parsed)
			return
		}

		if parsed.ButtonID == "btn_menu" || strings.EqualFold(strings.TrimSpace(parsed.RawText), "menu") || strings.EqualFold(strings.TrimSpace(parsed.RawText), "/menu") {
			typingMs := 1000 + rand.Intn(500)
			_ = r.waClient.SimulateTyping(ctx, parsed.SenderJID, time.Duration(typingMs)*time.Millisecond)
			r.handleMenu(ctx, parsed)
			return
		}

		// Forward text, complaint, or image to admin live support
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
	isAdmin := r.cfg.IsAdmin(p.SenderJID, p.SenderAltJID, p.ChatJID, p.SenderPhone)
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
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		_ = r.waClient.SendText(ctx, p.SenderJID, "Format tanggal salah. Gunakan format YYYY-MM-DD, contoh: 2026-10-01")
		return
	}

	expiry := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, time.UTC)
	err = r.billingService.SetMemberExpiry(ctx, phone, expiry)
	if err != nil {
		_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("Gagal mengatur masa aktif: %v", err))
		return
	}

	_ = r.waClient.SendText(ctx, p.SenderJID, fmt.Sprintf("✅ Masa aktif nomor *%s* berhasil diatur sampai *%s*.", phone, FormatDate(expiry, r.cfg.AppTimezone)))
}

// Live Support Handlers

func (r *Router) handleStartSupport(ctx context.Context, p *ParsedMessage) {
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
