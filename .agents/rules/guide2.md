---
trigger: always_on
---

23. Monthly Billing

Implement a background scheduler.

The scheduler should find subscriptions approaching their billing date/expiration.

Example:

subscription expires in 3 days

send:

«🔔 Reminder Tagihan

Masa aktif Google One kamu akan berakhir pada 4 Oktober.

Tagihan berikutnya: Rp23.000

[ 💳 Bayar Sekarang ]»

Reminder timing must be configurable.

Example:

REMINDER_DAYS_BEFORE_EXPIRATION=3

---

24. Reminder Idempotency

A scheduler may run multiple times.

It must NOT send duplicate reminders.

Create a reminder tracking mechanism.

Example:

subscription_id
billing_period
reminder_type
sent_at

Use unique constraints.

The same reminder period/type must only be sent once unless explicitly retried.

---

25. Billing Period

Each invoice must represent a specific billing period.

Example:

2026-09

Use a unique constraint such as:

subscriber_id + plan_id + billing_period

where appropriate.

This prevents duplicate invoices for the same billing cycle.

---

26. Database

Use PostgreSQL.

Suggested tables:

admins
subscribers
plans
subscriptions
invoices
payments
payment_proofs
reminders
audit_logs

Use proper:

- foreign keys
- indexes
- unique constraints
- timestamps
- transaction boundaries

Do not use an in-memory map as the source of truth.

Database is the source of truth.

---

27. Whatsmeow Integration

Use the actual current "go.mau.fi/whatsmeow" APIs.

Before writing integration code:

1. Inspect the exact dependency version.
2. Inspect official package documentation/source.
3. Identify the correct client initialization.
4. Identify authentication/session storage.
5. Identify message event handling.
6. Identify media download APIs.
7. Identify message sending APIs.
8. Identify JID structures.
9. Identify supported interactive message/protobuf structures.

Do NOT guess APIs.

If documentation and examples conflict with the installed version, prioritize the installed version and verify against source.

Keep WhatsApp-specific code isolated behind an interface.

Example conceptual interface:

type WhatsAppClient interface {
    SendText(...)
    SendImage(...)
    SendInteractive(...)
    DownloadMedia(...)
}

The exact implementation should follow actual "whatsmeow" APIs.

---

28. WhatsApp Event Handling

Create a dedicated event handler.

Conceptually:

WhatsApp Event
      ↓
Event Router
      ↓
Message Parser
      ↓
Command / Media Handler
      ↓
Billing Service

Do not put all logic into the WhatsApp event callback.

Handlers should delegate to domain/application services.

---

29. Concurrency

WhatsApp events may arrive concurrently.

The system must safely handle:

- duplicate messages
- duplicate button clicks
- simultaneous payment proof uploads
- simultaneous admin approval
- scheduler and user actions happening at the same time

Use:

- database transactions
- row locking where appropriate
- unique constraints
- idempotency keys/event IDs

Never rely solely on mutexes for database state.

---

30. Message Deduplication

If the same WhatsApp event is delivered more than once, the system must not:

- create duplicate invoice
- save duplicate payment
- extend subscription twice
- send duplicate confirmation

Persist a suitable WhatsApp message/event identifier when available.

---

31. Admin Notifications

When a new payment proof arrives, notify admins.

Example:

«🔔 Payment Proof Received

User: Fahad
Invoice: INV-202609-000001
Amount: Rp23.000

Status: PENDING_REVIEW

[ ✅ Approve ]
[ ❌ Reject ]»

Again, only implement native buttons if actually supported by the chosen "whatsmeow" version.

Otherwise send commands:

/approve INV-202609-000001
/reject INV-202609-000001 alasan

If sending the payment proof image to admin is technically feasible, forward/send the stored proof image along with the invoice details.

---

32. Audit Log

Record important administrative actions.

Examples:

INVOICE_CREATED
PROOF_SUBMITTED
INVOICE_APPROVED
INVOICE_REJECTED
SUBSCRIPTION_EXTENDED
REMINDER_SENT

Store:

actor
action
entity_type
entity_id
metadata
created_at

Do not store sensitive secrets.

---

33. Configuration

Use environment variables.

Example ".env.example":

APP_ENV=production
APP_TIMEZONE=Asia/Jakarta

DATABASE_URL=postgres://...

WHATSAPP_DB_PATH=/data/whatsapp

INVOICE_EXPIRATION_MINUTES=15

REMINDER_DAYS_BEFORE_EXPIRATION=3

PAYMENT_BANK_NAME=BCA
PAYMENT_ACCOUNT_NUMBER=
PAYMENT_ACCOUNT_NAME=

PROOF_STORAGE_PATH=/data/payment-proofs
MAX_PROOF_SIZE_MB=10

ADMIN_JIDS=

Never hardcode:

- database password
- admin JIDs
- bank details
- WhatsApp credentials/session paths
- secrets

---

34. Timezone

Default timezone:

Asia/Jakarta

Store timestamps in UTC when possible.

Convert only at presentation boundaries.

Invoice expiry must be calculated using absolute timestamps, not string-based local time comparisons.

---

35. Architecture

Prefer a clean modular monolith.

Suggested structure:

cmd/
  bot/
    main.go

internal/
  whatsapp/
    client.go
    events.go
    messages.go
    media.go

  billing/
    invoice.go
    payment.go
    subscription.go
    reminder.go

  subscriber/
    service.go

  admin/
    service.go

  scheduler/
    scheduler.go

  storage/
    postgres.go

  config/
    config.go

  domain/
    subscriber.go
    plan.go
    invoice.go
    payment.go
    subscription.go

migrations/

Dockerfile
docker-compose.yml

.env.example
README.md

Do not over-engineer with microservices.

One Go application is enough for the initial version.

---

36. Admin UI

Do NOT make an admin web panel mandatory for MVP.

Admin commands through WhatsApp are sufficient.

However, structure the services so a future admin web panel can call the same application/domain services.

Do not put business logic directly inside WhatsApp handlers.

---

37. Future Payment Gateway

Version 1 must NOT require a payment gateway.

But design payment handling so the future implementation can support:

ManualTransferProvider
PaymentGatewayProvider

Conceptually:

type PaymentProvider interface {
    CreatePayment(...)
    GetPaymentStatus(...)
}

For version 1:

ManualTransferProvider

may simply create a manual payment/invoice flow.

Future version:

QRISProvider
MidtransProvider
XenditProvider
etc.

Do not implement gateway registration/integration now.

---

38. Security

Implement:

- admin authorization
- input validation
- database constraints
- safe media handling
- rate limiting where appropriate
- structured logging
- no secret leakage
- safe error messages
- transaction boundaries
- idempotent payment approval
- protection against path traversal
- maximum media size
- protection against duplicate WhatsApp events

Do not log:

- passwords
- database credentials
- session secrets
- full sensitive payment proof paths if unnecessary

---

39. Rate Limiting / Abuse Protection

Prevent users from spamming:

bayar

and creating excessive operations.

Examples:

- reasonable command rate limit
- invoice creation cooldown
- proof upload rate limit
- admin command protection

Do not make limits so aggressive that normal usage becomes frustrating.

---

40. Error Handling

The bot must never crash because of:

- malformed WhatsApp messages
- unsupported media
- invalid commands
- expired invoice
- missing subscriber
- database transient failure
- failed media download
- duplicate event

Errors should be logged with useful context.

User-facing messages should remain simple.

---

41. Observability

Use structured logging.

Every important operation should include identifiers where available:

invoice_id
subscriber_id
whatsapp_message_id

Example:

invoice_created
invoice=INV-202609-000001
subscriber=123

Avoid logging secrets or payment proof contents.

---

42. Testing

Write tests for at least:

Invoice

- create invoice
- duplicate create returns existing active invoice
- expired invoice cannot receive proof
- invoice expiration
- invalid state transition

Payment

- proof submission
- duplicate proof handling
- approve payment
- reject payment
- double approval does not extend subscription twice

Subscription

- extension while active
- extension after expiry
- correct billing period

Authorization

- authorized admin
- unauthorized admin

WhatsApp

Mock the WhatsApp interface.

Test:

- incoming command
- incoming image
- unsupported message
- duplicate event

Scheduler

Test:

- reminder sent once
- reminder not duplicated
- expired invoices handled

---

43. Docker

Provide:

docker-compose.yml

with at least:

bot
postgres

Persistent volumes:

postgres_data
whatsapp_data
payment_proofs

The WhatsApp authentication/session data must survive container restarts.

Payment proofs must survive container restarts.

Database must survive container restarts.

---

44. Graceful Shutdown

Handle:

SIGTERM
SIGINT

Gracefully close:

- WhatsApp client
- database connections
- workers
- scheduler

Do not corrupt session/database state during shutdown.

---

45. README

Provide complete setup documentation:

Requirements
Installation
Environment variables
Database migration
WhatsApp authentication/pairing
Docker setup
Local development
Running tests
Admin configuration
Adding subscribers
Creating plans
Payment workflow
Troubleshooting

Explain how to pair/login the WhatsApp account using the actual "whatsmeow" implementation.

Do not invent authentication commands.

---

46. Development Strategy

Implement in phases.

Phase 1 — Foundation

- Go project
- configuration
- PostgreSQL
- migrations
- logging
- Docker

Phase 2 — WhatsApp

- whatsmeow integration
- authentication/session persistence
- incoming message events
- sending text
- media download

Phase 3 — Billing

- subscribers
- plans
- subscriptions
- invoices
- invoice expiration

Phase 4 — Payment Proof

- image detection
- media storage
- proof association
- PENDING_REVIEW

Phase 5 — Admin

- admin JID authorization
- "/payments"
- "/invoice"
- "/approve"
- "/reject"

Phase 6 — Automation

- monthly billing
- reminders
- idempotency
- scheduler

Phase 7 — Interactive UX

- native buttons if supported
- otherwise robust text fallback

Phase 8 — Hardening

- tests
- concurrency handling
- security
- observability
- graceful shutdown
- documentation

---

47. Critical Engineering Rules

These rules MUST be followed.

Rule 1

Never invent "whatsmeow" APIs.

Inspect the actual dependency/source before implementing.

Rule 2

Never assume native WhatsApp buttons are supported.

Verify the actual protobuf/message structures.

Rule 3

If buttons are unavailable:

Use text commands as fallback.

The application must still be fully usable.

Rule 4

The database is the source of truth.

Not WhatsApp messages.

Not memory.

Not scheduler state.

Rule 5

Invoice expiration must survive restarts.

Rule 6

Payment approval must be idempotent.

Rule 7

Subscription extension must happen exactly once per successful payment.

Rule 8

Payment proof is untrusted input.

Rule 9

Never require a customer-facing website for the MVP.

Rule 10

Do not implement payment gateway integration yet.

Only prepare clean abstractions for future integration.

Rule 11

Do not over-engineer.

A modular Go monolith is preferred.

Rule 12

Do not rewrite working code unnecessarily.

Inspect the existing repository first if one exists.

---