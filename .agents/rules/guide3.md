---
trigger: always_on
---

48. Target UX

The ideal UX is:

BOT

🔔 TAGIHAN BULANAN

Google One Family
Rp23.000
Jatuh tempo: 4 Oktober

[ 💳 Bayar Sekarang ]

User clicks.

BOT

🧾 INVOICE #INV-202609-000001

💰 Rp23.000

💳 BCA
123456789
a.n. Example

⏱ Invoice berlaku 15 menit.

Setelah transfer, kirim bukti pembayaran di chat ini.

[ 📤 Saya Sudah Transfer ]

User transfers.

User sends image.

BOT

📥 Bukti pembayaran diterima.

Invoice:
INV-202609-000001

Status:
⏳ MENUNGGU VERIFIKASI ADMIN

Mohon tunggu konfirmasi.

Admin approves.

BOT → USER

✅ PEMBAYARAN BERHASIL

Invoice:
INV-202609-000001

Nominal:
Rp23.000

Subscription:
Google One Family

Aktif sampai:
4 November 2026

Terima kasih 🙏

Everything above happens primarily through WhatsApp.

---

49. Definition of Done

The system is considered complete when:

- [ ] WhatsApp account can authenticate and persist session
- [ ] User can send "bayar"
- [ ] Bot can create invoice
- [ ] Invoice has 15-minute expiration
- [ ] Duplicate clicks do not create duplicate active invoices
- [ ] Bot displays payment instructions
- [ ] User can send payment proof directly through WhatsApp
- [ ] Image is downloaded and stored securely
- [ ] Invoice changes to "PENDING_REVIEW"
- [ ] Admin receives notification
- [ ] Admin can approve
- [ ] Admin can reject
- [ ] Approval is idempotent
- [ ] Subscription is extended exactly once
- [ ] User receives payment confirmation
- [ ] Expired invoices are automatically marked "EXPIRED"
- [ ] Monthly reminders work
- [ ] Reminder sending is idempotent
- [ ] Unauthorized users cannot execute admin commands
- [ ] WhatsApp duplicate events do not duplicate billing actions
- [ ] Data survives Docker restart
- [ ] Payment proofs survive Docker restart
- [ ] Tests cover critical billing flows
- [ ] README contains complete setup instructions
- [ ] Native buttons are used only if actually supported by the installed whatsmeow version
- [ ] Text-command fallback works regardless of button support
- [ ] No payment gateway is required

---

Final Instruction

Start by inspecting the repository and the exact "whatsmeow" version/API available.

Do not immediately generate large amounts of code.

First determine:

1. Current project structure
2. Go version
3. "whatsmeow" version
4. Available WhatsApp interactive message support
5. Authentication/session storage approach
6. Media download approach
7. Existing database/migration setup

Then propose the implementation plan.

After the plan is established, implement the system incrementally.

Prioritize correctness, idempotency, WhatsApp reliability, database integrity, and a smooth WhatsApp-first payment experience over unnecessary UI complexity.