package whatsapp

import (
	"fmt"
	"strings"
	"time"

	"wabill/internal/billing"
	"wabill/internal/domain"
	"wabill/internal/paymentprovider"
)

var indonesianMonths = map[time.Month]string{
	time.January:   "Januari",
	time.February:  "Februari",
	time.March:     "Maret",
	time.April:     "April",
	time.May:       "Mei",
	time.June:      "Juni",
	time.July:      "Juli",
	time.August:    "Agustus",
	time.September: "September",
	time.October:   "Oktober",
	time.November:  "November",
	time.December:  "Desember",
}

func FormatRupiah(amount int64) string {
	sign := ""
	if amount < 0 {
		sign = "-"
		amount = -amount
	}
	s := fmt.Sprintf("%d", amount)
	n := len(s)
	if n <= 3 {
		return fmt.Sprintf("%sRp%s", sign, s)
	}

	var parts []string
	remainder := n % 3
	if remainder > 0 {
		parts = append(parts, s[:remainder])
	}
	for i := remainder; i < n; i += 3 {
		parts = append(parts, s[i:i+3])
	}
	return fmt.Sprintf("%sRp%s", sign, strings.Join(parts, "."))
}

func FormatDate(t time.Time, loc *time.Location) string {
	local := t.In(loc)
	month := indonesianMonths[local.Month()]
	return fmt.Sprintf("%d %s %d", local.Day(), month, local.Year())
}

func FormatTime(t time.Time, loc *time.Location) string {
	local := t.In(loc)
	return local.Format("15:04")
}

func FormatMonthYear(period string) string {
	t, err := time.Parse("2006-01", period)
	if err != nil {
		return period
	}
	return fmt.Sprintf("%s %d", indonesianMonths[t.Month()], t.Year())
}

func FormatPhoneNumber(phone string) string {
	phone = strings.TrimSpace(phone)
	if strings.HasPrefix(phone, "62") {
		return "0" + phone[2:]
	}
	return phone
}

func TemplateMonthlyBill(name, planName string, amount int64, period string) string {
	return fmt.Sprintf(
`🔔 *Tagihan Bulanan*

Halo %s!
Tagihan %s bulan ini:

💰 *%s*
📅 Periode: %s

Ketik *bayar* atau klik tombol di bawah untuk melanjutkan.`,
		name, planName, FormatRupiah(amount), FormatMonthYear(period),
	)
}

func TemplateInvoice(inv *domain.Invoice, inst *paymentprovider.PaymentInstructions, loc *time.Location) string {
	return fmt.Sprintf(
`🧾 *INVOICE #%s*

💰 Total: *%s*
⏱ Berlaku sampai: *%s* (%s)

💳 Transfer ke:
*%s*
*%s*
a.n. %s

Setelah transfer, kirim foto/screenshot bukti pembayaran langsung di chat ini.

Status: ⏳ *Menunggu pembayaran*

_Ketik "transfer" jika sudah transfer, atau langsung upload foto bukti transfer._`,
		inv.InvoiceNumber,
		FormatRupiah(inv.Amount),
		FormatTime(inv.ExpiresAt, loc),
		FormatDate(inv.ExpiresAt, loc),
		inst.BankName,
		inst.AccountNumber,
		inst.AccountName,
	)
}

func TemplateWaitingProof() string {
	return "Silakan kirim foto atau screenshot bukti transfer langsung di chat ini ya! 📥"
}

func TemplateProofReceived(invoiceNumber string) string {
	return fmt.Sprintf(
`📥 *Bukti pembayaran diterima!*

Invoice: *#%s*
Status: ⏳ *Menunggu verifikasi admin*

Mohon tunggu konfirmasi ya. Kamu akan mendapat pesan lagi setelah pembayaran selesai diverifikasi 🙏`,
		invoiceNumber,
	)
}

func TemplatePaymentSuccess(inv *domain.Invoice, planName string, expiresAt time.Time, loc *time.Location) string {
	return fmt.Sprintf(
`✅ *PEMBAYARAN BERHASIL*

Invoice: *#%s*
Nominal: *%s*
Subscription: *%s*
Aktif sampai: *%s*

Terima kasih atas pembayarannya! 🙏`,
		inv.InvoiceNumber,
		FormatRupiah(inv.Amount),
		planName,
		FormatDate(expiresAt, loc),
	)
}

func TemplatePaymentRejected(invoiceNumber, reason string) string {
	return fmt.Sprintf(
`❌ *Pembayaran belum dapat diverifikasi.*

Invoice: *#%s*

Alasan:
%s

Silakan buat invoice baru dengan mengetik *bayar* dan lakukan pembayaran kembali.`,
		invoiceNumber, reason,
	)
}

func TemplateCustomerStatus(sub *domain.Subscriber, plan *domain.Plan, subscription *domain.Subscription, loc *time.Location) string {
	now := time.Now().UTC()
	statusText := "✅ ACTIVE"
	if subscription == nil || !subscription.IsActive(now) {
		statusText = "⚠️ EXPIRED / INACTIVE"
	}

	planName := "Shared Plan"
	price := int64(0)
	if plan != nil {
		planName = plan.Name
		price = plan.Price
	}

	expiryStr := "-"
	if subscription != nil {
		expiryStr = FormatDate(subscription.ExpiresAt, loc)
	}

	msg := fmt.Sprintf(
`👤 *Status Langganan*

Nama: *%s*
📦 Plan: *%s*
📊 Status: *%s*
⏰ Aktif sampai: *%s*
💰 Harga bulanan: *%s*`,
		sub.Name, planName, statusText, expiryStr, FormatRupiah(price),
	)

	if subscription == nil || !subscription.IsActive(now) {
		msg += "\n\nKetik *bayar* untuk mengaktifkan atau memperpanjang langganan."
	}
	return msg
}

func TemplateCustomerHistory(invoices []*domain.Invoice, loc *time.Location) string {
	if len(invoices) == 0 {
		return "📜 *Riwayat Pembayaran*\n\nBelum ada riwayat transaksi."
	}

	var sb strings.Builder
	sb.WriteString("📜 *Riwayat Pembayaran*\n\n")
	for i, inv := range invoices {
		statusIcon := "⏳"
		if inv.Status == domain.InvoicePaid {
			statusIcon = "✅"
		} else if inv.Status == domain.InvoiceRejected {
			statusIcon = "❌"
		} else if inv.Status == domain.InvoiceExpired {
			statusIcon = "⌛"
		}

		sb.WriteString(fmt.Sprintf("%d. *%s* (%s)\n   Nominal: %s\n   Status: %s %s\n\n",
			i+1,
			inv.InvoiceNumber,
			FormatMonthYear(inv.BillingPeriod),
			FormatRupiah(inv.Amount),
			statusIcon,
			inv.Status,
		))
	}
	return strings.TrimSpace(sb.String())
}

func TemplateCustomerMenu() string {
	return `🤖 *Menu Layanan WhatsApp Billing*

Ketik salah satu perintah berikut:
• *bayar* — Bayar tagihan / buat invoice baru
• *tagihan* — Lihat status invoice aktif
• *status* — Cek masa aktif langganan
• *riwayat* — Lihat riwayat pembayaran terakhir
• *bantuan* — Hubungi Admin / Customer Support (Live Chat)
• *menu* — Tampilkan menu layanan ini

Atau ketik angka:
1️⃣ Bayar Sekarang
2️⃣ Cek Status
3️⃣ Riwayat
4️⃣ Menu Utama
5️⃣ Hubungi Admin / Bantuan`
}

func TemplateAdminMenu() string {
	return `👑 *Panel Menu Admin (WhatsApp Billing)*

Daftar perintah khusus Admin:
• */members* — Daftar semua member & masa aktif
• */addmember <nomor> <nama> [paket] [tgl_siklus]* — Tambah member baru
• */delmember <nomor>* — Nonaktifkan member
• */aktifkan <nomor>* — Aktifkan kembali member yang nonaktif
• */setexpiry <nomor> <YYYY-MM-DD>* — Atur masa aktif manual (misal tgl 1)
• */payments* — Daftar bukti transfer menunggu approval
• */invoice <NO_INV>* — Detail invoice
• */approve <NO_INV>* — Verifikasi & approve pembayaran
• */reject <NO_INV> <alasan>* — Tolak pembayaran dengan alasan
• */reply <nomor> <pesan>* — Balas pesan live support member
• */closesession <nomor>* — Tutup sesi live support member`
}

func TemplateMenu(isAdmin bool) string {
	custMenu := TemplateCustomerMenu()
	if !isAdmin {
		// Customer hanya menerima menu layanan mereka sendiri
		return custMenu
	}

	return custMenu + "\n\n---\n" + TemplateAdminMenu() + "\n\n💡 _Catatan: Bagian Menu Admin di atas HANYA tampil untuk Anda sebagai Admin. Customer biasa TIDAK BISA melihat menu admin._"
}

func TemplateHelp(isAdmin bool) string {
	return TemplateMenu(isAdmin)
}

func TemplateSupportWelcome(name string) string {
	return fmt.Sprintf(
`💬 *Sesi Bantuan Admin Terhubung*

Halo %s! Kamu sekarang terhubung langsung dalam sesi live support bersama Admin.
Silakan tulis pertanyaan, kendala, atau keluhan kamu di chat ini (bisa berupa teks maupun foto/screenshot).

Setiap pesan yang kamu kirim akan langsung diteruskan ke Admin kami.

_Ketik *selesai* atau klik tombol di bawah kapan saja untuk mengakhiri sesi bantuan._`,
		name,
	)
}

func TemplateAdminSupportForward(subName, subPhone, inquiry string) string {
	formattedPhone := FormatPhoneNumber(subPhone)
	return fmt.Sprintf(
		"📩 *Pesan Bantuan Masuk dari Member!*\n\n" +
			"👤 Member: *%s*\n" +
			"📱 Nomor: *%s*\n" +
			"💬 Pesan:\n\"%s\"\n\n" +
			"_Untuk membalas pesan ini ke member, ketik:_\n" +
			"`/reply %s [pesan balasan]`\n" +
			"_Untuk menutup sesi:_\n" +
			"`/closesession %s`",
		subName, formattedPhone, inquiry, subPhone, subPhone,
	)
}

func TemplateCustomerAdminReply(replyText string) string {
	return fmt.Sprintf(
`🧑‍💻 *Balasan dari Admin:*

%s

---
_Ketik *selesai* jika kendala kamu sudah terselesaikan._`,
		replyText,
	)
}

func TemplateSupportClosedCustomer() string {
	return `✅ *Sesi bantuan telah ditutup.*

Terima kasih telah menghubungi kami! Jika kamu membutuhkan bantuan lain di kemudian hari, ketik *bantuan* kapan saja. Semoga harimu menyenangkan! 🙏`
}

func TemplateSupportClosedAdmin(subName, subPhone string) string {
	return fmt.Sprintf("✅ Sesi bantuan untuk member *%s* (%s) telah ditutup.", subName, FormatPhoneNumber(subPhone))
}

func TemplateAdminMemberList(members []billing.MemberDetails, filter string, loc *time.Location) (string, []ButtonOption) {
	var filtered []billing.MemberDetails
	for _, m := range members {
		if m.Subscriber == nil {
			continue
		}
		switch filter {
		case "inactive":
			if m.Subscriber.Status != domain.SubscriberActive {
				filtered = append(filtered, m)
			}
		case "all":
			filtered = append(filtered, m)
		default: // "active"
			if m.Subscriber.Status == domain.SubscriberActive {
				filtered = append(filtered, m)
			}
		}
	}

	var title string
	var emptyMsg string
	var buttons []ButtonOption

	switch filter {
	case "inactive":
		title = fmt.Sprintf("👥 *Daftar Member Nonaktif (%d)*:\n\n", len(filtered))
		emptyMsg = "Tidak ada member yang sedang nonaktif (semua member aktif)."
		buttons = []ButtonOption{
			{ID: "btn_members_active", Text: "🟢 Member Aktif"},
			{ID: "btn_members_all", Text: "📋 Semua Member"},
		}
	case "all":
		title = fmt.Sprintf("👥 *Semua Daftar Member (%d)*:\n\n", len(filtered))
		emptyMsg = "Belum ada member yang terdaftar di sistem."
		buttons = []ButtonOption{
			{ID: "btn_members_active", Text: "🟢 Member Aktif"},
			{ID: "btn_members_inactive", Text: "🔴 Member Nonaktif"},
		}
	default: // "active"
		title = fmt.Sprintf("👥 *Daftar Member Aktif (%d)*:\n\n", len(filtered))
		emptyMsg = "Belum ada member aktif yang terdaftar."
		buttons = []ButtonOption{
			{ID: "btn_members_inactive", Text: "🔴 Member Nonaktif"},
			{ID: "btn_members_all", Text: "📋 Semua Member"},
		}
	}

	if len(filtered) == 0 {
		return fmt.Sprintf("%s%s\n\n💡 _Perintah Admin:_\n• `/addmember <nomor> <nama> [paket] [tgl_siklus]`\n• `/aktifkan <nomor>`", title, emptyMsg), buttons
	}

	var sb strings.Builder
	sb.WriteString(title)
	for i, m := range filtered {
		statusIcon := "🟢"
		if m.Subscriber.Status != domain.SubscriberActive {
			statusIcon = "🔴"
		}
		planName := "Shared Plan"
		if m.Plan != nil {
			planName = m.Plan.Name
		}
		expiryStr := "Belum aktif"
		if m.Subscription != nil {
			expiryStr = FormatDate(m.Subscription.ExpiresAt, loc)
		}

		sb.WriteString(fmt.Sprintf("%d. %s *%s* (%s)\n   Plan: %s\n   Aktif sampai: %s\n   Status: %s\n\n",
			i+1, statusIcon, m.Subscriber.Name, FormatPhoneNumber(m.Subscriber.PhoneNumber), planName, expiryStr, m.Subscriber.Status))
	}
	sb.WriteString("💡 _Perintah Admin:_\n• `/addmember <nomor> <nama> [paket] [tgl_siklus]`\n• `/delmember <nomor>` (nonaktifkan)\n• `/aktifkan <nomor>` (aktifkan kembali)\n• `/setexpiry <nomor> <YYYY-MM-DD>`")
	return strings.TrimSpace(sb.String()), buttons
}

func TemplateAdminNewProof(subName, subPhone, invNumber string, amount int64) string {
	return fmt.Sprintf(
`🔔 *Bukti Pembayaran Baru Diterima!*

User: *%s* (%s)
Invoice: *#%s*
Nominal: *%s*

Status: ⏳ *PENDING_REVIEW*

Untuk verifikasi pembayaran:
Ketik:
/approve %s
atau
/reject %s [alasan]`,
		subName, FormatPhoneNumber(subPhone), invNumber, FormatRupiah(amount), invNumber, invNumber,
	)
}

func TemplateAdminPendingList(invoices []*domain.Invoice) string {
	if len(invoices) == 0 {
		return "✅ Tidak ada pembayaran yang sedang menunggu verifikasi (PENDING_REVIEW)."
	}

	var sb strings.Builder
	sb.WriteString("📋 *Daftar Menunggu Approval (PENDING_REVIEW):*\n\n")
	for i, inv := range invoices {
		sb.WriteString(fmt.Sprintf("%d. *#%s*\n   Nominal: %s\n   Periode: %s\n   Perintah: `/approve %s`\n\n",
			i+1, inv.InvoiceNumber, FormatRupiah(inv.Amount), inv.BillingPeriod, inv.InvoiceNumber,
		))
	}
	return strings.TrimSpace(sb.String())
}

func TemplateReminder(subName, planName string, expiresAt time.Time, amount int64, loc *time.Location) string {
	return fmt.Sprintf(
`🔔 *Reminder Tagihan*

Halo %s!
Masa aktif %s kamu akan berakhir pada *%s*.

Tagihan berikutnya: *%s*

Ketik *bayar* untuk melakukan perpanjangan sekarang.`,
		subName, planName, FormatDate(expiresAt, loc), FormatRupiah(amount),
	)
}
