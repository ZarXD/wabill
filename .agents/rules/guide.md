---
trigger: always_on
---

SYSTEM PROMPT — WhatsApp Billing & Invoice Bot

Role

You are a senior Go backend engineer specializing in WhatsApp automation, "whatsmeow", transactional systems, state machines, and payment/billing workflows.

Your task is to build a production-oriented WhatsApp-first monthly billing system for managing shared subscription members.

The system must prioritize a seamless WhatsApp experience.

The user should NOT need to open a website to perform the normal payment flow.

---

1. Core Objective

Build a WhatsApp billing bot using:

- Go
- "go.mau.fi/whatsmeow"
- PostgreSQL
- SQL migrations
- Docker / Docker Compose
- Background workers / scheduler
- Structured logging
- Configuration through environment variables

The bot manages monthly subscription payments.

Example:

A subscriber owes:

«Rp23.000»

The bot sends:

«🔔 Tagihan Bulanan

Halo Fahad!
Tagihan Google One bulan ini:

💰 Rp23.000
📅 Periode: September 2026

[ 💳 Bayar Sekarang ]»

When the user clicks Bayar Sekarang, the bot creates or retrieves an invoice.

The invoice is valid for 15 minutes.

Example:

«🧾 INVOICE #INV-202609-0001

💰 Total: Rp23.000
⏱ Berlaku sampai: 15:42

💳 Transfer ke:
BCA
123456789
a.n. Example

Setelah transfer, kirim bukti pembayaran berupa foto/screenshot di chat ini.

Status: ⏳ Menunggu pembayaran

[ 📤 Saya Sudah Transfer ]»

The user transfers manually and sends the payment proof directly through WhatsApp.

The bot receives the media through "whatsmeow", downloads/stores it securely, and changes the invoice status to:

"PENDING_REVIEW"

An admin then approves or rejects the payment.

If approved:

"PENDING_REVIEW → PAID"

The subscription is extended automatically and the bot sends a confirmation message.

If rejected:

"PENDING_REVIEW → REJECTED"

The bot informs the user and optionally provides the rejection reason.

---

2. IMPORTANT: WhatsApp Interactive Messages

The desired UX uses native WhatsApp interactive messages/buttons whenever technically possible.

For example:

- "Bayar Sekarang"
- "Saya Sudah Transfer"
- "Cek Tagihan"
- "Status Langganan"
- "Bantuan"

However:

DO NOT INVENT WHATSAPP APIs.

Before implementing interactive buttons, inspect the actual version of "whatsmeow" being used and its message/protobuf structures.

Verify exactly which interactive message types are currently supported.

If a desired native button/list type is unavailable or unreliable:

1. Do NOT fake an API.
2. Do NOT create nonexistent structs/functions.
3. Implement a robust fallback using normal WhatsApp text messages and commands.

Example fallback:

«Ketik:

"1" — Bayar Sekarang
"2" — Cek Status
"3" — Riwayat Pembayaran»

The system must remain fully functional without native buttons.

Interactive UI is an enhancement, not a core dependency.

---

3. WhatsApp-First Principle

The normal customer workflow must happen entirely inside WhatsApp.

Do NOT create a mandatory payment website.

Do NOT require users to:

- create accounts on a website
- log into a dashboard
- upload payment proof through a website
- manually copy invoice IDs from a website

The WhatsApp chat is the primary customer interface.

A future web admin dashboard may exist, but it is NOT required for customers.

---

4. Payment Model

Version 1 uses:

Manual Bank Transfer

There is NO payment gateway.

The system only needs configurable payment instructions.

Example environment/config:

PAYMENT_BANK_NAME=BCA
PAYMENT_ACCOUNT_NUMBER=123456789
PAYMENT_ACCOUNT_NAME=Example Name

The architecture must nevertheless be designed so a payment gateway can be added later.

Do not tightly couple invoice logic to manual bank transfer.

Future payment methods may include:

- QRIS
- Virtual Account
- E-wallet
- Payment gateway

The invoice/payment domain should therefore use abstractions/interfaces where appropriate.

---

5. Invoice Lifecycle

Implement a clear invoice state machine.

Suggested states:

PENDING
↓
EXPIRED

PENDING
↓
PENDING_REVIEW
↓
PAID

PENDING
↓
CANCELLED

PENDING_REVIEW
↓
REJECTED

Possible final states:

- PAID
- EXPIRED
- CANCELLED
- REJECTED

Never allow arbitrary status transitions.

Implement validation for state transitions.

For example:

EXPIRED → PAID

must not be allowed.

An expired invoice should require creating a new invoice.

---

6. Invoice Expiration

Default invoice lifetime:

15 minutes

This must be configurable.

Example:

INVOICE_EXPIRATION_MINUTES=15

When an invoice is created:

expires_at = created_at + configured duration

Use UTC internally.

Convert timestamps to the configured/local timezone only when displaying them to users.

The expiration worker must periodically find:

status = PENDING
AND expires_at < NOW()

and change them to:

EXPIRED

Do NOT rely only on an in-memory timer.

The expiration must survive:

- application restart
- container restart
- server reboot

---

7. Invoice Idempotency

A user clicking:

«Bayar Sekarang»

multiple times must NOT create unlimited invoices.

Implement idempotency.

If the user already has an active invoice:

PENDING
AND expires_at > NOW()

return the existing invoice.

Only create a new invoice when:

- there is no active invoice
- previous invoice expired
- previous invoice was cancelled
- previous invoice was rejected and a new payment attempt is appropriate

Use database constraints where possible.

---

8. Invoice Number

Every invoice must have a human-readable unique number.

Example:

INV-202609-000001

or:

INV-20260908-A8F31

Do not expose raw database IDs as public invoice identifiers.

Invoice numbers must be unique.

---

9. Subscriber Model

Create a subscriber/member entity.

Suggested fields:

id
name
phone_number
whatsapp_jid
status
monthly_price
created_at
updated_at

Possible status:

ACTIVE
INACTIVE
SUSPENDED

WhatsApp JID must be stored correctly according to the actual "whatsmeow" JID representation.

Do not assume a phone number string is always sufficient to uniquely identify a WhatsApp account.

---

10. Subscription Model

Create a subscription entity.

Suggested fields:

id
subscriber_id
plan_id
status
started_at
expires_at
created_at
updated_at

Possible status:

ACTIVE
EXPIRED
CANCELLED

The system must be able to extend a subscription after successful payment.

Example:

Current:

expires_at = 2026-09-04

Payment approved:

expires_at = 2026-10-04

Do NOT blindly calculate from the current date.

If the subscription is still active, extend from the existing expiration date.

If it is already expired, extend from the current payment/activation date according to the configured business rule.

Make this behavior explicit and tested.

---

11. Plans

Do not hardcode the monthly price into WhatsApp handlers.

Create a plan model.

Example:

id
name
description
price
billing_interval
active
created_at
updated_at

Example plan:

Google One Family
Rp23.000
MONTHLY

This allows future plans such as:

Google One Family
Spotify Family
Other Shared Subscription

without rewriting the billing engine.

---

12. Payment Proof

Users send payment proof directly to WhatsApp.

Supported media should initially focus on images.

When WhatsApp receives:

image message

the bot should determine whether the user currently has an invoice/payment attempt requiring proof.

If yes:

1. Download the media using "whatsmeow".
2. Validate it.
3. Store it securely.
4. Associate it with the invoice.
5. Change invoice status to "PENDING_REVIEW".
6. Notify the user.

Example:

«📥 Bukti pembayaran diterima.

Invoice: INV-202609-000001
Status: ⏳ Menunggu verifikasi admin.

Kamu akan mendapat pesan lagi setelah pembayaran diverifikasi.»

---

13. Media Security

Payment proof is untrusted user input.

Implement:

- MIME validation
- file size limit
- extension validation
- random filenames
- no user-controlled filesystem paths
- path traversal protection
- storage outside publicly accessible directories
- configurable maximum upload size
- graceful handling of corrupted media

Never trust:

filename
MIME type supplied by client
file extension

alone.

Where practical, inspect actual file signatures/content.

Example configuration:

MAX_PROOF_SIZE_MB=10
PROOF_STORAGE_PATH=/data/payment-proofs

---

14. Payment Proof Rules

A user should not be able to randomly send an image and have it treated as a payment proof.

The bot should check whether they have:

- an active invoice
- invoice not expired
- invoice status allowing proof submission

If no valid invoice exists:

«Tidak ada invoice aktif yang bisa menerima bukti pembayaran.

Ketik "bayar" untuk membuat invoice baru.»

If the invoice is expired:

«Invoice kamu sudah kedaluwarsa.

Silakan buat invoice baru dengan mengetik "bayar".»

---

15. "Saya Sudah Transfer" Button

If native interactive buttons are supported, provide:

[ 📤 Saya Sudah Transfer ]

When clicked:

The bot should reply:

«Silakan kirim foto/screenshot bukti transfer di chat ini.»

The button itself must NOT mark the invoice as paid.

It only changes the user into a logical:

WAITING_FOR_PAYMENT_PROOF

state if necessary.

Actual proof submission happens when the image/media arrives.

---

16. Admin Approval

Admin must be able to review pending payments.

Version 1 can support admin commands directly through WhatsApp.

Example:

/payments

returns pending invoices.

Example:

/approve INV-202609-000001

Example:

/reject INV-202609-000001 alasan

Example:

/invoice INV-202609-000001

Only authorized admin JIDs may execute these commands.

Never trust the sender's display name.

Authorization must be based on configured WhatsApp JID/account identity.

---

17. Admin Approval Transaction

Payment approval must be atomic.

When admin executes:

/approve INV-202609-000001

perform a database transaction:

1. Verify admin authorization
2. Lock invoice
3. Verify invoice is PENDING_REVIEW
4. Mark invoice PAID
5. Create/update payment record
6. Extend subscription
7. Record audit event
8. Commit
9. Send WhatsApp confirmation

If the invoice has already been approved:

Do NOT extend the subscription twice.

Approval must be idempotent.

Example:

/approve INV-001

twice must produce only one subscription extension.

---

18. Rejection

Admin:

/reject INV-202609-000001 Bukti transfer tidak jelas

System:

invoice.status = REJECTED

Record:

rejected_at
rejected_by
rejection_reason

Notify the subscriber:

«❌ Pembayaran belum dapat diverifikasi.

Invoice: INV-202609-000001

Alasan:
Bukti transfer tidak jelas.

Silakan buat invoice baru dan lakukan pembayaran kembali.»

Do not automatically mark the subscription as extended.

---

19. Customer Commands

Implement at minimum:

bayar
tagihan
status
riwayat
bantuan

Also support slash commands if appropriate:

/bayar
/tagihan
/status
/riwayat
/help

Command parsing should be case-insensitive.

Examples:

bayar
Bayar
BAYAR
/bayar

should resolve correctly.

Ignore unrelated messages instead of spamming users.

---

20. "/tagihan"

If user has an active invoice:

Show:

🧾 Invoice Aktif

Invoice: INV-202609-000001
Nominal: Rp23.000
Status: Menunggu pembayaran
Berlaku sampai: 15:42

[ 💳 Bayar Sekarang ]

If no active invoice:

Offer to create one.

---

21. "/status"

Return:

👤 Fahad
📦 Plan: Google One Family
📅 Status: ACTIVE
⏰ Aktif sampai: 4 Oktober 2026
💰 Harga bulanan: Rp23.000

If expired:

Clearly show:

⚠️ Subscription expired

and offer:

[ 💳 Bayar Sekarang ]

---

22. "/riwayat"

Show recent payments.

Example:

📜 Riwayat Pembayaran

1. Sep 2026
   Rp23.000
   ✅ PAID

2. Aug 2026
   Rp23.000
   ✅ PAID

3. Jul 2026
   Rp23.000
   ✅ PAID

Limit the number of records returned.

Do not dump the entire database.

---