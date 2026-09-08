package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
	"wabill/internal/config"
)

type SessionManager struct {
	container *sqlstore.Container
	client    *whatsmeow.Client
	cfg       *config.Config
}

// InitWhatsmeowClient initializes the PostgreSQL session container and whatsmeow client.
func InitWhatsmeowClient(ctx context.Context, db *sql.DB, cfg *config.Config) (*SessionManager, error) {
	dbLog := waLog.Stdout("Database", "WARN", true)
	clientLog := waLog.Stdout("WhatsApp", "INFO", true)

	container := sqlstore.NewWithDB(db, "postgres", dbLog)
	if err := container.Upgrade(ctx); err != nil {
		return nil, fmt.Errorf("failed to upgrade whatsmeow database: %w", err)
	}

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get whatsmeow device: %w", err)
	}

	cli := whatsmeow.NewClient(deviceStore, clientLog)

	mgr := &SessionManager{
		container: container,
		client:    cli,
		cfg:       cfg,
	}

	return mgr, nil
}

// ConnectAndPair handles login via QR code if not already paired, or connects directly.
func (s *SessionManager) ConnectAndPair(ctx context.Context) error {
	if s.client.Store.ID == nil {
		// No existing session: initiate QR code pairing
		qrChan, _ := s.client.GetQRChannel(ctx)
		err := s.client.Connect()
		if err != nil {
			return fmt.Errorf("failed to connect WhatsApp client: %w", err)
		}

		log.Println("=================================================================")
		log.Println("Silakan scan QR code berikut menggunakan aplikasi WhatsApp kamu:")
		log.Println("=================================================================")

		go func() {
			for evt := range qrChan {
				if evt.Event == "code" {
					qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
					log.Println(">> Scan QR Code di atas menggunakan WhatsApp (Perangkat Tertaut)")
				} else {
					log.Printf("[WhatsApp Login] Event: %s\n", evt.Event)
				}
			}
		}()
	} else {
		// Existing session found, connect directly
		log.Println("[WhatsApp] Sesi WhatsApp lama ditemukan, menyambungkan kembali...")
		if err := s.client.Connect(); err != nil {
			return fmt.Errorf("failed to connect WhatsApp with existing session: %w", err)
		}
		log.Println("[WhatsApp] Berhasil terhubung ke WhatsApp!")
	}

	return nil
}

func (s *SessionManager) Client() *whatsmeow.Client {
	return s.client
}

func (s *SessionManager) Close() error {
	s.client.Disconnect()
	return nil
}
