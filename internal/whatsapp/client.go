package whatsapp

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"wabill/internal/config"
)

// WhatsAppClient provides an abstracted interface for sending messages and downloading media.
type WhatsAppClient interface {
	SendText(ctx context.Context, to string, text string) error
	SendButtons(ctx context.Context, to string, text string, buttons []ButtonOption) error
	SendList(ctx context.Context, to string, text string, footer string, buttonText string, sections []ListSection) error
	SendImage(ctx context.Context, to string, imageData []byte, caption, mimeType string) error
	DownloadImage(ctx context.Context, imgMsg *waE2E.ImageMessage) ([]byte, error)
	MarkRead(ctx context.Context, chat, sender types.JID, msgIDs []types.MessageID, timestamp time.Time) error
	SimulateTyping(ctx context.Context, to string, duration time.Duration) error
	IsConnected() bool
	Disconnect()
}

type WhatsmeowClient struct {
	cli *whatsmeow.Client
	cfg *config.Config
}

func NewWhatsmeowClient(cli *whatsmeow.Client, cfg *config.Config) *WhatsmeowClient {
	return &WhatsmeowClient{
		cli: cli,
		cfg: cfg,
	}
}

func (w *WhatsmeowClient) SendText(ctx context.Context, to string, text string) error {
	toJID, err := types.ParseJID(to)
	if err != nil {
		return fmt.Errorf("invalid recipient JID: %w", err)
	}

	msg := &waE2E.Message{
		Conversation: proto.String(text),
	}

	_, err = w.cli.SendMessage(ctx, toJID, msg)
	if err != nil {
		return fmt.Errorf("failed to send text message: %w", err)
	}
	return nil
}

func (w *WhatsmeowClient) SendButtons(ctx context.Context, to string, text string, buttons []ButtonOption) error {
	toJID, err := types.ParseJID(to)
	if err != nil {
		return fmt.Errorf("invalid recipient JID: %w", err)
	}

	if w.cfg.EnableNativeButtons && len(buttons) > 0 {
		// 1. Attempt modern NativeFlowMessage with Baileys-style binary additionalNodes
		additionalNodes := []waBinary.Node{
			{
				Tag:   "biz",
				Attrs: waBinary.Attrs{},
				Content: []waBinary.Node{
					{
						Tag: "interactive",
						Attrs: waBinary.Attrs{
							"type": "native_flow",
							"v":    "1",
						},
						Content: []waBinary.Node{
							{
								Tag: "native_flow",
								Attrs: waBinary.Attrs{
									"v":    "9",
									"name": "mixed",
								},
							},
						},
					},
				},
			},
			{
				Tag: "bot",
				Attrs: waBinary.Attrs{
					"biz_bot": "1",
				},
			},
		}

		nativeFlowMsg := BuildNativeFlowButtonsMessage(text, buttons)
		resp, err := w.cli.SendMessage(ctx, toJID, nativeFlowMsg, whatsmeow.SendRequestExtra{
			AdditionalNodes: &additionalNodes,
		})
		if err == nil {
			log.Printf("[WhatsApp] Sent native flow buttons with additionalNodes successfully to %s (msgID=%s)", to, resp.ID)
			return nil
		}
		log.Printf("[WhatsApp] Native flow button failed (%v), trying direct ButtonsMessage", err)

		// 2. Attempt legacy ButtonsMessage
		btnMsg := BuildButtonsMessage(text, buttons)
		_, err = w.cli.SendMessage(ctx, toJID, btnMsg)
		if err == nil {
			log.Printf("[WhatsApp] Sent direct ButtonsMessage to %s", to)
			return nil
		}
		log.Printf("[WhatsApp] Direct ButtonsMessage failed (%v), falling back to text", err)
	}

	// Fallback to beautiful text format
	fallbackText := FormatButtonsTextFallback(text, buttons)
	return w.SendText(ctx, to, fallbackText)
}

func (w *WhatsmeowClient) SendList(ctx context.Context, to string, text string, footer string, buttonText string, sections []ListSection) error {
	toJID, err := types.ParseJID(to)
	if err != nil {
		return fmt.Errorf("invalid recipient JID: %w", err)
	}

	if w.cfg.EnableNativeButtons && len(sections) > 0 {
		listMsg := BuildListMessage(text, footer, buttonText, sections)
		_, err := w.cli.SendMessage(ctx, toJID, listMsg)
		if err == nil {
			log.Printf("[WhatsApp] Sent native ListMessage to %s", to)
			return nil
		}
		log.Printf("[WhatsApp] Native ListMessage failed (%v), falling back to text", err)
	}

	var sb strings.Builder
	sb.WriteString(text)
	sb.WriteString("\n\n")
	for _, sec := range sections {
		sb.WriteString(fmt.Sprintf("*%s*\n", sec.Title))
		for idx, row := range sec.Rows {
			sb.WriteString(fmt.Sprintf("%d. %s - %s (Ketik: %s)\n", idx+1, row.Title, row.Description, row.ID))
		}
	}
	return w.SendText(ctx, to, strings.TrimSpace(sb.String()))
}

func (w *WhatsmeowClient) SendImage(ctx context.Context, to string, imageData []byte, caption, mimeType string) error {
	toJID, err := types.ParseJID(to)
	if err != nil {
		return fmt.Errorf("invalid recipient JID: %w", err)
	}

	uploadResp, err := w.cli.Upload(ctx, imageData, whatsmeow.MediaImage)
	if err != nil {
		return fmt.Errorf("failed to upload image: %w", err)
	}

	imgMsg := &waE2E.ImageMessage{
		Caption:       proto.String(caption),
		Mimetype:      proto.String(mimeType),
		URL:           &uploadResp.URL,
		DirectPath:    &uploadResp.DirectPath,
		MediaKey:      uploadResp.MediaKey,
		FileEncSHA256: uploadResp.FileEncSHA256,
		FileSHA256:    uploadResp.FileSHA256,
		FileLength:    &uploadResp.FileLength,
	}

	msg := &waE2E.Message{ImageMessage: imgMsg}
	_, err = w.cli.SendMessage(ctx, toJID, msg)
	if err != nil {
		return fmt.Errorf("failed to send image message: %w", err)
	}
	return nil
}

func (w *WhatsmeowClient) DownloadImage(ctx context.Context, imgMsg *waE2E.ImageMessage) ([]byte, error) {
	if imgMsg == nil {
		return nil, fmt.Errorf("image message is nil")
	}
	return w.cli.Download(ctx, imgMsg)
}

func (w *WhatsmeowClient) MarkRead(ctx context.Context, chat, sender types.JID, msgIDs []types.MessageID, timestamp time.Time) error {
	if w.cli == nil || len(msgIDs) == 0 {
		return nil
	}
	return w.cli.MarkRead(ctx, msgIDs, timestamp, chat, sender)
}

func (w *WhatsmeowClient) SimulateTyping(ctx context.Context, to string, duration time.Duration) error {
	if w.cli == nil {
		return nil
	}
	toJID, err := types.ParseJID(to)
	if err != nil {
		return err
	}
	_ = w.cli.SendChatPresence(ctx, toJID, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	select {
	case <-time.After(duration):
	case <-ctx.Done():
		_ = w.cli.SendChatPresence(ctx, toJID, types.ChatPresencePaused, types.ChatPresenceMediaText)
		return ctx.Err()
	}
	_ = w.cli.SendChatPresence(ctx, toJID, types.ChatPresencePaused, types.ChatPresenceMediaText)
	return nil
}

func (w *WhatsmeowClient) IsConnected() bool {
	return w.cli != nil && w.cli.IsConnected()
}

func (w *WhatsmeowClient) Disconnect() {
	if w.cli != nil {
		w.cli.Disconnect()
	}
}
