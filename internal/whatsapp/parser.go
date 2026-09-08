package whatsapp

import (
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
)

type CommandType string

const (
	CmdUnknown            CommandType = "UNKNOWN"
	CmdBayar              CommandType = "BAYAR"
	CmdTagihan            CommandType = "TAGIHAN"
	CmdStatus             CommandType = "STATUS"
	CmdRiwayat            CommandType = "RIWAYAT"
	CmdMenu               CommandType = "MENU"
	CmdSupport            CommandType = "SUPPORT"
	CmdCloseSupport       CommandType = "CLOSE_SUPPORT"
	CmdHelp               CommandType = CmdMenu
	CmdAlreadyTransferred CommandType = "ALREADY_TRANSFERRED"
	CmdAdminPayments      CommandType = "ADMIN_PAYMENTS"
	CmdAdminInvoice       CommandType = "ADMIN_INVOICE"
	CmdAdminApprove       CommandType = "ADMIN_APPROVE"
	CmdAdminReject        CommandType = "ADMIN_REJECT"
	CmdAdminMembers       CommandType = "ADMIN_MEMBERS"
	CmdAdminAddMember     CommandType = "ADMIN_ADD_MEMBER"
	CmdAdminDelMember     CommandType = "ADMIN_DEL_MEMBER"
	CmdAdminSetExpiry     CommandType = "ADMIN_SET_EXPIRY"
	CmdAdminReply         CommandType = "ADMIN_REPLY"
	CmdAdminCloseSupport   CommandType = "ADMIN_CLOSE_SUPPORT"
	CmdAdminMenu           CommandType = "ADMIN_MENU"
	CmdAdminActivateMember CommandType = "ADMIN_ACTIVATE_MEMBER"
)

type ParsedMessage struct {
	MessageID    string
	SenderJID    string
	SenderAltJID string
	ChatJID      string
	SenderPhone  string
	PushName     string
	IsFromMe     bool
	IsGroup      bool
	RawText      string
	Command      CommandType
	CommandArgs  []string
	ImageMessage *waE2E.ImageMessage
	ButtonID     string
}

// ParseIncomingMessage extracts the sender, message text, button ID, or media from a WhatsApp message event.
func ParseIncomingMessage(evt *events.Message) *ParsedMessage {
	if evt == nil || evt.Message == nil {
		return nil
	}

	// Extract normalized JID
	senderJID := evt.Info.Sender.ToNonAD().String()
	phone := evt.Info.Sender.User

	var altJID string
	if !evt.Info.SenderAlt.IsEmpty() {
		altJID = evt.Info.SenderAlt.ToNonAD().String()
		if evt.Info.Sender.Server == "lid" && evt.Info.SenderAlt.Server == "s.whatsapp.net" {
			phone = evt.Info.SenderAlt.User
		}
	}

	chatJID := evt.Info.Chat.ToNonAD().String()
	if (phone == "" || evt.Info.Sender.Server == "lid") && evt.Info.Chat.Server == "s.whatsapp.net" {
		phone = evt.Info.Chat.User
	}

	parsed := &ParsedMessage{
		MessageID:    evt.Info.ID,
		SenderJID:    senderJID,
		SenderAltJID: altJID,
		ChatJID:      chatJID,
		SenderPhone:  phone,
		PushName:     evt.Info.PushName,
		IsFromMe:     evt.Info.IsFromMe,
		IsGroup:      evt.Info.IsGroup,
		Command:      CmdUnknown,
	}

	msg := evt.Message

	// Unwrap wrapper messages (disappearing ephemeral messages, view once messages)
	for msg != nil {
		if msg.EphemeralMessage != nil && msg.EphemeralMessage.Message != nil {
			msg = msg.EphemeralMessage.Message
		} else if msg.ViewOnceMessage != nil && msg.ViewOnceMessage.Message != nil {
			msg = msg.ViewOnceMessage.Message
		} else if msg.ViewOnceMessageV2 != nil && msg.ViewOnceMessageV2.Message != nil {
			msg = msg.ViewOnceMessageV2.Message
		} else if msg.DocumentWithCaptionMessage != nil && msg.DocumentWithCaptionMessage.Message != nil {
			msg = msg.DocumentWithCaptionMessage.Message
		} else {
			break
		}
	}

	if msg == nil {
		return nil
	}

	// Silently ignore non-chat protocol messages, reactions, receipts, poll updates, etc.
	if msg.ProtocolMessage != nil || msg.ReactionMessage != nil || msg.EncReactionMessage != nil || msg.PollUpdateMessage != nil {
		return nil
	}

	// Extract text or button clicks from various WhatsApp protobuf wrappers
	switch {
	case msg.Conversation != nil:
		parsed.RawText = *msg.Conversation
	case msg.ExtendedTextMessage != nil && msg.ExtendedTextMessage.Text != nil:
		parsed.RawText = *msg.ExtendedTextMessage.Text
	case msg.ButtonsResponseMessage != nil:
		if msg.ButtonsResponseMessage.SelectedButtonID != nil {
			parsed.ButtonID = *msg.ButtonsResponseMessage.SelectedButtonID
			parsed.RawText = parsed.ButtonID
		}
	case msg.TemplateButtonReplyMessage != nil:
		if msg.TemplateButtonReplyMessage.SelectedID != nil {
			parsed.ButtonID = *msg.TemplateButtonReplyMessage.SelectedID
			parsed.RawText = parsed.ButtonID
		}
	case msg.InteractiveResponseMessage != nil:
		if resp := msg.InteractiveResponseMessage.GetNativeFlowResponseMessage(); resp != nil {
			if resp.ParamsJSON != nil {
				parsed.RawText = *resp.ParamsJSON
			}
		}
	case msg.ListResponseMessage != nil:
		if msg.ListResponseMessage.SingleSelectReply != nil && msg.ListResponseMessage.SingleSelectReply.SelectedRowID != nil {
			parsed.ButtonID = *msg.ListResponseMessage.SingleSelectReply.SelectedRowID
			parsed.RawText = parsed.ButtonID
		}
	case msg.ImageMessage != nil:
		parsed.ImageMessage = msg.ImageMessage
		if msg.ImageMessage.Caption != nil {
			parsed.RawText = *msg.ImageMessage.Caption
		}
	}

	// Discard messages that have no text, no button, and no image
	if strings.TrimSpace(parsed.RawText) == "" && parsed.ButtonID == "" && parsed.ImageMessage == nil {
		return nil
	}

	// Resolve text to logical command
	resolveCommand(parsed)

	return parsed
}

func resolveCommand(p *ParsedMessage) {
	text := strings.TrimSpace(p.RawText)
	if text == "" && p.ImageMessage == nil {
		return
	}

	parts := strings.Fields(text)
	if len(parts) == 0 {
		return
	}

	firstWord := strings.ToLower(parts[0])
	firstWord = strings.TrimPrefix(firstWord, "/")

	if len(parts) > 1 {
		p.CommandArgs = parts[1:]
	}

	switch firstWord {
	case "bayar", "1", "btn_bayar":
		p.Command = CmdBayar
	case "tagihan", "invoice", "inv", "tagih":
		// Check if it's admin "/invoice <INV>"
		if len(p.CommandArgs) > 0 && strings.HasPrefix(strings.ToUpper(p.CommandArgs[0]), "INV-") {
			p.Command = CmdAdminInvoice
		} else {
			p.Command = CmdTagihan
		}
	case "status", "2", "btn_status", "langganan":
		p.Command = CmdStatus
	case "riwayat", "3", "btn_riwayat", "history":
		p.Command = CmdRiwayat
	case "menu", "help", "4", "btn_menu":
		p.Command = CmdMenu
	case "halo", "hi", "hai", "helo", "hello", "p":
		if len(parts) == 1 {
			p.Command = CmdMenu
		} else if len(parts) == 2 {
			second := strings.ToLower(parts[1])
			if second == "admin" || second == "min" || second == "cs" {
				p.Command = CmdSupport
			} else if second == "bot" {
				p.Command = CmdMenu
			}
		}
	case "bantuan", "support", "cs", "btn_bantuan", "5":
		p.Command = CmdSupport
	case "selesai", "exit", "tutup", "btn_selesai":
		p.Command = CmdCloseSupport
	case "transfer", "btn_transfer", "sudah transfer", "saya sudah transfer":
		p.Command = CmdAlreadyTransferred
	case "payments", "pending":
		p.Command = CmdAdminPayments
	case "approve", "acc":
		p.Command = CmdAdminApprove
	case "reject", "tolak":
		p.Command = CmdAdminReject
	case "members", "subscribers", "pelanggan", "member":
		p.Command = CmdAdminMembers
	case "addmember", "tambahmember", "addsub":
		p.Command = CmdAdminAddMember
	case "delmember", "hapusmember", "delsub", "nonaktifkan":
		p.Command = CmdAdminDelMember
	case "activate", "aktifkan", "aktifkanmember":
		p.Command = CmdAdminActivateMember
	case "setexpiry", "setkadaluarsa", "aturtgl":
		p.Command = CmdAdminSetExpiry
	case "reply", "balas":
		p.Command = CmdAdminReply
	case "closesession", "tutupcs", "endsession":
		p.Command = CmdAdminCloseSupport
	case "admin", "menuadmin", "adminmenu":
		p.Command = CmdAdminMenu
	default:
		// Check exact button IDs
		if p.ButtonID != "" {
			switch p.ButtonID {
			case "btn_bayar":
				p.Command = CmdBayar
			case "btn_transfer":
				p.Command = CmdAlreadyTransferred
			case "btn_status":
				p.Command = CmdStatus
			case "btn_riwayat":
				p.Command = CmdRiwayat
			case "btn_menu", "btn_help":
				p.Command = CmdMenu
			case "btn_bantuan":
				p.Command = CmdSupport
			case "btn_selesai":
				p.Command = CmdCloseSupport
			case "btn_members_active":
				p.Command = CmdAdminMembers
				p.CommandArgs = []string{"active"}
			case "btn_members_inactive":
				p.Command = CmdAdminMembers
				p.CommandArgs = []string{"inactive"}
			case "btn_members_all":
				p.Command = CmdAdminMembers
				p.CommandArgs = []string{"all"}
			}
		}
	}
}
