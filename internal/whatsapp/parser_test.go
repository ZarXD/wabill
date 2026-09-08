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
