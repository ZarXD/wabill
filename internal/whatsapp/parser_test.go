package whatsapp_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"wabill/internal/whatsapp"
)

func TestParseIncomingMessage(t *testing.T) {
	testJID := types.NewJID("628123456789", types.DefaultUserServer)

	tests := []struct {
		name        string
		evt         *events.Message
		wantCommand whatsapp.CommandType
		wantArgsLen int
	}{
		{
			name: "Regular text 'bayar'",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "MSG1",
				},
				Message: &waE2E.Message{
					Conversation: proto.String("bayar"),
				},
			},
			wantCommand: whatsapp.CmdBayar,
		},
		{
			name: "Slash command '/Bayar'",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "MSG2",
				},
				Message: &waE2E.Message{
					Conversation: proto.String("/Bayar"),
				},
			},
			wantCommand: whatsapp.CmdBayar,
		},
		{
			name: "Quick number '1'",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "MSG3",
				},
				Message: &waE2E.Message{
					Conversation: proto.String("1"),
				},
			},
			wantCommand: whatsapp.CmdBayar,
		},
		{
			name: "Button response 'btn_bayar'",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "MSG4",
				},
				Message: &waE2E.Message{
					ButtonsResponseMessage: &waE2E.ButtonsResponseMessage{
						SelectedButtonID: proto.String("btn_bayar"),
					},
				},
			},
			wantCommand: whatsapp.CmdBayar,
		},
		{
			name: "Admin command '/approve INV-202609-001'",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "MSG5",
				},
				Message: &waE2E.Message{
					Conversation: proto.String("/approve INV-202609-001"),
				},
			},
			wantCommand: whatsapp.CmdAdminApprove,
			wantArgsLen: 1,
		},
		{
			name: "Admin command '/reject INV-202609-001 Bukti buram'",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "MSG6",
				},
				Message: &waE2E.Message{
					Conversation: proto.String("/reject INV-202609-001 Bukti buram"),
				},
			},
			wantCommand: whatsapp.CmdAdminReject,
			wantArgsLen: 3,
		},
		{
			name: "Customer command 'menu'",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "MSG7",
				},
				Message: &waE2E.Message{
					Conversation: proto.String("menu"),
				},
			},
			wantCommand: whatsapp.CmdMenu,
		},
		{
			name: "Customer command 'bantuan'",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "MSG8",
				},
				Message: &waE2E.Message{
					Conversation: proto.String("bantuan"),
				},
			},
			wantCommand: whatsapp.CmdSupport,
		},
		{
			name: "Customer command 'selesai'",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "MSG9",
				},
				Message: &waE2E.Message{
					Conversation: proto.String("selesai"),
				},
			},
			wantCommand: whatsapp.CmdCloseSupport,
		},
		{
			name: "Customer greeting 'halo'",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "MSG9_HALO",
				},
				Message: &waE2E.Message{
					Conversation: proto.String("halo"),
				},
			},
			wantCommand: whatsapp.CmdMenu,
		},
		{
			name: "Customer greeting 'halo admin'",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "MSG9_HALO_ADMIN",
				},
				Message: &waE2E.Message{
					Conversation: proto.String("halo admin"),
				},
			},
			wantCommand: whatsapp.CmdSupport,
			wantArgsLen: 1,
		},
		{
			name: "Admin command '/reply 08123456789 Halo'",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "MSG10",
				},
				Message: &waE2E.Message{
					Conversation: proto.String("/reply 08123456789 Halo kak, ada apa?"),
				},
			},
			wantCommand: whatsapp.CmdAdminReply,
			wantArgsLen: 5,
		},
		{
			name: "Admin command '/closesession 08123456789'",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "MSG11",
				},
				Message: &waE2E.Message{
					Conversation: proto.String("/closesession 08123456789"),
				},
			},
			wantCommand: whatsapp.CmdAdminCloseSupport,
			wantArgsLen: 1,
		},
		{
			name: "Admin menu command '/admin'",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "MSG12",
				},
				Message: &waE2E.Message{
					Conversation: proto.String("/admin"),
				},
			},
			wantCommand: whatsapp.CmdAdminMenu,
		},
		{
			name: "Admin activate member command '/aktifkan 08123456789'",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "MSG13",
				},
				Message: &waE2E.Message{
					Conversation: proto.String("/aktifkan 08123456789"),
				},
			},
			wantCommand: whatsapp.CmdAdminActivateMember,
			wantArgsLen: 1,
		},
		{
			name: "Button response 'btn_members_inactive'",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "MSG14",
				},
				Message: &waE2E.Message{
					ButtonsResponseMessage: &waE2E.ButtonsResponseMessage{
						SelectedButtonID: proto.String("btn_members_inactive"),
					},
				},
			},
			wantCommand: whatsapp.CmdAdminMembers,
			wantArgsLen: 1,
		},
		{
			name: "Ephemeral disappearing message unwrapping",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "MSG15",
				},
				Message: &waE2E.Message{
					EphemeralMessage: &waE2E.FutureProofMessage{
						Message: &waE2E.Message{
							Conversation: proto.String("status"),
						},
					},
				},
			},
			wantCommand: whatsapp.CmdStatus,
			wantArgsLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := whatsapp.ParseIncomingMessage(tt.evt)
			if parsed == nil {
				t.Fatalf("expected parsed message, got nil")
			}
			if parsed.Command != tt.wantCommand {
				t.Errorf("Command = %v, want %v", parsed.Command, tt.wantCommand)
			}
			if len(parsed.CommandArgs) != tt.wantArgsLen {
				t.Errorf("CommandArgs len = %v, want %v", len(parsed.CommandArgs), tt.wantArgsLen)
			}
		})
	}
}

func TestParseIncomingMessageIgnored(t *testing.T) {
	testJID := types.NewJID("628123456789", types.DefaultUserServer)

	ignoredCases := []struct {
		name string
		evt  *events.Message
	}{
		{
			name: "Reaction message should be ignored",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "REACTION_1",
				},
				Message: &waE2E.Message{
					ReactionMessage: &waE2E.ReactionMessage{
						Text: proto.String("👍"),
					},
				},
			},
		},
		{
			name: "Protocol message should be ignored",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "PROTO_1",
				},
				Message: &waE2E.Message{
					ProtocolMessage: &waE2E.ProtocolMessage{},
				},
			},
		},
		{
			name: "Empty text message should be ignored",
			evt: &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{Sender: testJID},
					ID:            "EMPTY_1",
				},
				Message: &waE2E.Message{
					Conversation: proto.String("   "),
				},
			},
		},
	}

	for _, tt := range ignoredCases {
		t.Run(tt.name, func(t *testing.T) {
			parsed := whatsapp.ParseIncomingMessage(tt.evt)
			if parsed != nil {
				t.Errorf("expected parsed message to be nil for %s, got %+v", tt.name, parsed)
			}
		})
	}
}

func TestPiketCommands(t *testing.T) {
	groupJID := types.NewJID("120363123456789012", "g.us")
	senderJID := types.NewJID("628123456789", types.DefaultUserServer)

	piketTests := []struct {
		name        string
		text        string
		wantCommand whatsapp.CommandType
	}{
		{"Check JID /jid", "/jid", whatsapp.CmdJID},
		{"Check JID /cekid", "/cekid", whatsapp.CmdJID},
		{"View Piket", "piket", whatsapp.CmdPiket},
		{"View Jadwal Lele", "jadwal lele", whatsapp.CmdPiket},
		{"Tambah Piket", "/tambahpiket @6281111111", whatsapp.CmdTambahPiket},
		{"Ganti Piket", "/gantipiket @6282222222", whatsapp.CmdGantiPiket},
		{"List Piket", "/listpiket", whatsapp.CmdListPiket},
		{"Hapus Piket", "/hapuspiket 1", whatsapp.CmdHapusPiket},
		{"Sudah Pakan 'sudah'", "sudah", whatsapp.CmdSudahPakan},
		{"Sudah Pakan '/done'", "/done", whatsapp.CmdSudahPakan},
	}

	for _, tt := range piketTests {
		t.Run(tt.name, func(t *testing.T) {
			evt := &events.Message{
				Info: types.MessageInfo{
					MessageSource: types.MessageSource{
						Chat:    groupJID,
						Sender:  senderJID,
						IsGroup: true,
					},
					ID: "MSG_PIKET",
				},
				Message: &waE2E.Message{
					Conversation: proto.String(tt.text),
				},
			}
			parsed := whatsapp.ParseIncomingMessage(evt)
			if parsed == nil {
				t.Fatalf("expected non-nil parsed message")
			}
			if parsed.Command != tt.wantCommand {
				t.Errorf("expected command %v, got %v", tt.wantCommand, parsed.Command)
			}
		})
	}
}

func TestMentionExtraction(t *testing.T) {
	groupJID := types.NewJID("120363123456789012", "g.us")
	senderJID := types.NewJID("628123456789", types.DefaultUserServer)

	t.Run("ContextInfo MentionedJID extraction", func(t *testing.T) {
		evt := &events.Message{
			Info: types.MessageInfo{
				MessageSource: types.MessageSource{
					Chat:    groupJID,
					Sender:  senderJID,
					IsGroup: true,
				},
				ID: "MSG_MENTION_1",
			},
			Message: &waE2E.Message{
				ExtendedTextMessage: &waE2E.ExtendedTextMessage{
					Text: proto.String("/tambahpiket @Budi @Fahad"),
					ContextInfo: &waE2E.ContextInfo{
						MentionedJID: []string{"6281111111@s.whatsapp.net", "6282222222@s.whatsapp.net"},
					},
				},
			},
		}

		parsed := whatsapp.ParseIncomingMessage(evt)
		if parsed == nil {
			t.Fatalf("expected non-nil parsed message")
		}
		if len(parsed.MentionedJIDs) != 2 {
			t.Fatalf("expected 2 mentioned JIDs, got %d", len(parsed.MentionedJIDs))
		}
		if parsed.MentionedJIDs[0] != "6281111111@s.whatsapp.net" || parsed.MentionedJIDs[1] != "6282222222@s.whatsapp.net" {
			t.Errorf("unexpected mentioned JIDs: %+v", parsed.MentionedJIDs)
		}
	})

	t.Run("Raw text @phone extraction fallback", func(t *testing.T) {
		evt := &events.Message{
			Info: types.MessageInfo{
				MessageSource: types.MessageSource{
					Chat:    groupJID,
					Sender:  senderJID,
					IsGroup: true,
				},
				ID: "MSG_MENTION_2",
			},
			Message: &waE2E.Message{
				Conversation: proto.String("/tambahpiket @081234567890"),
			},
		}

		parsed := whatsapp.ParseIncomingMessage(evt)
		if parsed == nil {
			t.Fatalf("expected non-nil parsed message")
		}
		if len(parsed.MentionedJIDs) != 1 {
			t.Fatalf("expected 1 mentioned JID extracted from raw text, got %d", len(parsed.MentionedJIDs))
		}
		if parsed.MentionedJIDs[0] != "6281234567890@s.whatsapp.net" {
			t.Errorf("expected 6281234567890@s.whatsapp.net, got %s", parsed.MentionedJIDs[0])
		}
	})
}

