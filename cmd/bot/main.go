package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wabill/internal/billing"
	"wabill/internal/config"
	"wabill/internal/paymentprovider"
	"wabill/internal/scheduler"
	"wabill/internal/storage"
	"wabill/internal/whatsapp"
)

func main() {
	log.Println("=========================================================")
	log.Println("           wabill - WhatsApp Billing Bot                 ")
	log.Println("=========================================================")

	// 1. Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[Main] Failed to load configuration: %v", err)
	}
	log.Printf("[Main] Configuration loaded. Timezone: %s, Admins: %v", cfg.TimezoneName, cfg.AdminJIDs)

	// 2. Connect to PostgreSQL
	db, err := storage.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("[Main] Failed to connect to database: %v", err)
	}
	defer db.Close()
	log.Println("[Main] Connected to PostgreSQL successfully")

	// 3. Run database migrations
	migCtx, migCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := db.RunMigrations(migCtx); err != nil {
		migCancel()
		log.Fatalf("[Main] Database migration failed: %v", err)
	}
	migCancel()

	// 4. Initialize storage repositories
	subRepo := storage.NewSubscriberRepo(db)
	subscriptionRepo := storage.NewSubscriptionRepo(db)
	invRepo := storage.NewInvoiceRepo(db)
	payRepo := storage.NewPaymentRepo(db)
	remRepo := storage.NewReminderRepo(db)
	auditRepo := storage.NewAuditRepo(db)
	dedupRepo := storage.NewDeduplicationRepo(db)

	// 5. Initialize payment provider
	provider := paymentprovider.NewManualTransferProvider(cfg)

	// 6. Initialize billing domain service
	billingSvc := billing.NewService(
		cfg,
		subRepo,
		subscriptionRepo,
		invRepo,
		payRepo,
		remRepo,
		auditRepo,
		provider,
	)

	// 7. Initialize WhatsApp session & client
	waCtx, waCancel := context.WithTimeout(context.Background(), 30*time.Second)
	sessionMgr, err := whatsapp.InitWhatsmeowClient(waCtx, db.DB, cfg)
	waCancel()
	if err != nil {
		log.Fatalf("[Main] Failed to initialize WhatsApp client: %v", err)
	}
	defer sessionMgr.Close()

	waClient := whatsapp.NewWhatsmeowClient(sessionMgr.Client(), cfg)

	// 8. Register message event router
	router := whatsapp.NewRouter(cfg, billingSvc, waClient, dedupRepo)
	sessionMgr.Client().AddEventHandler(router.HandleEvent)

	// 9. Connect & Pair WhatsApp
	pairCtx := context.Background()
	if err := sessionMgr.ConnectAndPair(pairCtx); err != nil {
		log.Fatalf("[Main] WhatsApp connect failed: %v", err)
	}

	// 10. Start background scheduler
	sched := scheduler.NewScheduler(cfg, billingSvc, waClient)
	sched.Start()

	log.Println("[Main] wabill is up and running! Press CTRL+C to exit.")

	// 11. Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("\n[Main] Shutdown signal received, terminating gracefully...")
	sched.Stop()
	router.Close()
	sessionMgr.Close()
	log.Println("[Main] wabill stopped cleanly. Goodbye!")
}
