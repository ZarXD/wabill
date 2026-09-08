# 🚀 wabill — WhatsApp-First Monthly Billing & Invoice Bot

A robust, production-ready WhatsApp billing and monthly subscription management bot built in **Go** using [`go.mau.fi/whatsmeow`](https://github.com/tulir/whatsmeow) and **PostgreSQL**.

The system manages recurring monthly shared subscriptions (e.g., *Google One Family*, *Spotify Family*) directly inside WhatsApp. Customers never need to visit an external website or create an account.

---

## ✨ Key Features

- **WhatsApp-First UX**: Invoice creation, payment instructions, proof upload, live support, and confirmation all happen entirely inside the WhatsApp chat.
- **Native Flow Interactive Buttons**: Sends modern WhatsApp quick-reply buttons (`[💳 Bayar Sekarang]`, `[📊 Cek Status]`, `[💬 Hubungi Admin]`, etc.) with binary XML node injection (`<biz>` + `<bot biz_bot="1"/>`), with 100% reliable fallback on all devices via numbered text quick-actions (`1`, `2`, `bayar`).
- **Live Support Session (Proxy Chat)**:
  - Customers typing `bantuan` / `support` / `cs` enter a direct live support session.
  - Inquiries, complaints, and screenshots are automatically forwarded to all configured `ADMIN_JIDS` with member identity details.
  - Admins can reply directly via WhatsApp using `/reply <nomor> <pesan>`.
  - Sessions can be ended anytime via `selesai` or `/closesession <nomor>`.
- **Anti-Spam & Anti-Ban Protection**:
  - **User Rate Limiter**: Leaky bucket / windowed rate limiter per user.
  - **Read Receipts**: Sends immediate blue ticks (`MarkRead`).
  - **Human-like Typing Presence**: Simulates natural typing delays (`SimulateTyping`) before sending replies.
  - **Keyed Mutex**: Sequential per-user processing lock to eliminate race conditions and double-click glitches.
- **Customer Privacy Whitelist**:
  - Only registered active subscribers and admins are processed.
  - Non-registered numbers (personal friends, family chatting on the bot's number) are silently ignored, keeping personal chats 100% private.
- **Fixed Billing Cycle Alignment**:
  - Recurring subscriptions lock to their designated cycle day of the month (e.g. 1st of the month) rather than sliding to arbitrary payment days.
- **15-Minute Invoice Expiration**: Invoices expire automatically after a configurable duration (default: 15 minutes). Expiration worker periodically scans database and survives container restarts.
- **Idempotency**:
  - Repeatedly clicking or typing `bayar` returns the existing active pending invoice instead of creating duplicate invoices.
  - Payment approval by admin is atomic (`SELECT ... FOR UPDATE`) and idempotent (double approvals never extend subscriptions twice).
  - WhatsApp duplicate events are discarded via message deduplication (`processed_events`).
- **Secure Payment Proof Handling**:
  - Validates file size (max 10MB configurable) and magic bytes MIME signature (`image/jpeg`, `image/png`, `image/webp`).
  - Stored securely on disk using randomized UUID filenames (`<uuid>.jpg`).
  - Automatically forwards proof screenshots to configured admins for review.
- **Member Management inside WhatsApp**:
  - `/members` — View active members with one-tap toggle buttons (`[ 🔴 Member Nonaktif ]`, `[ 📋 Semua Member ]`).
  - `/addmember <nomor> <nama> [paket] [tgl_siklus]` — Register a new member with a specified monthly cycle billing date.
  - `/delmember <nomor>` — Soft-deactivate a member without losing their payment history.
  - `/aktifkan <nomor>` — Reactivate a previously deactivated member.
  - `/setexpiry <nomor> <YYYY-MM-DD>` — Manually align or adjust a member's expiry date.
  - `/admin` — Dedicated Admin Command Panel.
- **Automated Background Scheduler**:
  - Periodically marks overdue pending invoices as `EXPIRED`.
  - Sends billing reminders before subscription expiration with idempotency tracking.
- **Persistent Storage**:
  - WhatsApp session is stored in PostgreSQL (`whatsmeow_device`), surviving container restarts.
  - Embedded SQL migrations applied automatically on startup.

---

## 🏛 State Machine

```
              ┌──────────────┐
              │   PENDING    │
              └──┬──────┬───┬┘
      15m expiry │      │   │ Cancelled
                 ▼      │   ▼
            ┌─────────┐ │ ┌───────────┐
            │ EXPIRED │ │ │ CANCELLED │
            └─────────┘ │ └───────────┘
     Customer uploads   │
     proof screenshot   ▼
            ┌────────────────┐
            │ PENDING_REVIEW │
            └──┬───────────┬─┘
       Admin   │           │ Admin
     /approve  ▼           ▼ /reject
            ┌──────┐   ┌──────────┐
            │ PAID │   │ REJECTED │
            └──────┘   └──────────┘
```

---

## 📋 Commands Reference

### 👤 Customer Commands (Case-insensitive, slash optional):
| Command | Number | Description |
| :--- | :---: | :--- |
| `bayar` | `1` | Creates or retrieves active invoice with bank transfer details |
| `status` | `2` | Displays active plan, subscription expiry, and monthly price |
| `riwayat` | `3` | Displays recent payment history |
| `menu` / `help` | `4` | Displays customer service catalog (cleanly hides admin commands) |
| `bantuan` / `support` / `cs` | `5` | Initiates real-time live support proxy chat with Admin |
| `selesai` / `exit` | — | Ends an ongoing live support session |
| `tagihan` | — | Shows status of current invoice |
| `transfer` | — | Prompts user to send photo/screenshot payment proof |

### 👑 Admin Commands (Restricted to `ADMIN_JIDS`):
| Command | Description |
| :--- | :--- |
| `/admin` | Displays dedicated Admin Command Panel |
| `/members` | Lists active members (with buttons for `[ 🔴 Member Nonaktif ]` and `[ 📋 Semua Member ]`) |
| `/addmember <nomor> <nama> [paket] [tgl_siklus]` | Adds a new member with designated monthly cycle date |
| `/delmember <nomor>` | Deactivates a member (preserves historical payment records) |
| `/aktifkan <nomor>` | Reactivates a deactivated member |
| `/setexpiry <nomor> <YYYY-MM-DD>` | Manually sets or aligns a member's expiry date |
| `/payments` | Lists all invoices pending verification (`PENDING_REVIEW`) |
| `/invoice <INV_NUMBER>` | Displays invoice details and inspects payment proof photo |
| `/approve <INV_NUMBER>` | Atomically approves payment, extends subscription, and notifies user |
| `/reject <INV_NUMBER> <reason>` | Rejects payment with a reason and notifies customer |
| `/reply <nomor> <pesan>` | Sends reply directly to a customer in live support session |
| `/closesession <nomor>` | Ends a customer's live support session from admin side |

---

## 🛠 Prerequisites

- **Go 1.24+** (if running locally)
- **Docker & Docker Compose** (recommended for production)
- **PostgreSQL 15+**

---

## ⚙️ Configuration (`.env`)

Copy `.env.example` to `.env` and fill in your values:

```bash
cp .env.example .env
```

| Variable | Default | Description |
| :--- | :--- | :--- |
| `APP_ENV` | `production` | Environment (`development` / `production`) |
| `APP_TIMEZONE` | `Asia/Jakarta` | Local timezone for displaying invoice dates |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5432/wabill?sslmode=disable` | PostgreSQL connection string |
| `INVOICE_EXPIRATION_MINUTES` | `15` | Minutes before an unpaid invoice expires |
| `REMINDER_DAYS_BEFORE_EXPIRATION` | `3` | Days before subscription expiry to send reminder |
| `PAYMENT_BANK_NAME` | `BCA` | Bank name for manual transfer |
| `PAYMENT_ACCOUNT_NUMBER` | `1234567890` | Account number for transfer |
| `PAYMENT_ACCOUNT_NAME` | `Your Name` | Account holder name |
| `PROOF_STORAGE_PATH` | `/data/payment-proofs` | Directory to store uploaded proofs |
| `MAX_PROOF_SIZE_MB` | `10` | Maximum upload size for proof images in MB |
| `ADMIN_JIDS` | `628123456789@s.whatsapp.net` | Comma-separated WhatsApp JIDs for admin commands |
| `ENABLE_NATIVE_BUTTONS` | `true` | Try native interactive buttons (auto-falls back to text) |

---

## 🚀 Running with Docker Compose (Recommended)

1. Ensure `.env` is configured.
2. Start the services:
   ```bash
   docker compose up -d
   ```
3. Open the container logs to scan the QR Code for WhatsApp pairing:
   ```bash
   docker compose logs -f bot
   ```
4. Scan the QR code in your terminal with WhatsApp (**Linked Devices** / **Perangkat Tertaut**).
5. Once paired, the session is saved permanently in PostgreSQL (`whatsmeow_device`) and survives container restarts.

---

## 💻 Local Development & Testing

1. Ensure a PostgreSQL instance is running:
   ```bash
   docker run --name wabill_pg -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=wabill -p 5432:5432 -d postgres:16-alpine
   ```
2. Run database migrations and start the bot:
   ```bash
   go run ./cmd/bot
   ```
3. Run all tests:
   ```bash
   go test -v ./...
   ```

---

## 🛡 Security & Reliability

- **Row Locking**: Payment approval uses `SELECT ... FOR UPDATE` transactions to prevent race conditions.
- **Untrusted File Hardening**: Uploaded proof files are inspected via magic bytes (`http.DetectContentType`), size-checked, and stored in an isolated filesystem path with randomized UUIDs to prevent directory traversal.
- **Anti-Spam & Anti-Ban**: Integrated rate limiting, sequential mutex locking per user, natural typing simulation, and immediate read receipts ensure bot health and account safety.
- **No Website Required**: The entire customer experience remains inside WhatsApp.

---

## 📄 License

MIT License.
