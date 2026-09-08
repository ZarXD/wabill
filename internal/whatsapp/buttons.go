package whatsapp

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"go.mau.fi/whatsmeow/proto/waE2E"
)

type ButtonOption struct {
	ID   string
	Text string
}

type ListRow struct {
	ID          string
	Title       string
	Description string
}

type ListSection struct {
	Title string
	Rows  []ListRow
}

// BuildButtonsMessage constructs a direct waE2E.ButtonsMessage (max 3 buttons).
func BuildButtonsMessage(bodyText string, buttons []ButtonOption) *waE2E.Message {
	var protoButtons []*waE2E.ButtonsMessage_Button
	for _, btn := range buttons {
		protoButtons = append(protoButtons, &waE2E.ButtonsMessage_Button{
			ButtonID: proto.String(btn.ID),
			ButtonText: &waE2E.ButtonsMessage_Button_ButtonText{
				DisplayText: proto.String(btn.Text),
			},
			Type: waE2E.ButtonsMessage_Button_RESPONSE.Enum(),
		})
	}

	return &waE2E.Message{
		ButtonsMessage: &waE2E.ButtonsMessage{
			ContentText: proto.String(bodyText),
			FooterText:  proto.String("wabill - WhatsApp Billing"),
			HeaderType:  waE2E.ButtonsMessage_EMPTY.Enum(),
			Buttons:     protoButtons,
		},
	}
}

// BuildListMessage constructs a waE2E.ListMessage with a popup menu button.
func BuildListMessage(bodyText, footer, buttonText string, sections []ListSection) *waE2E.Message {
	var protoSections []*waE2E.ListMessage_Section
	for _, sec := range sections {
		var protoRows []*waE2E.ListMessage_Row
		for _, r := range sec.Rows {
			protoRows = append(protoRows, &waE2E.ListMessage_Row{
				RowID:       proto.String(r.ID),
				Title:       proto.String(r.Title),
				Description: proto.String(r.Description),
			})
		}
		protoSections = append(protoSections, &waE2E.ListMessage_Section{
			Title: proto.String(sec.Title),
			Rows:  protoRows,
		})
	}

	return &waE2E.Message{
		ListMessage: &waE2E.ListMessage{
			Description: proto.String(bodyText),
			FooterText:  proto.String(footer),
			ButtonText:  proto.String(buttonText),
			ListType:    waE2E.ListMessage_SINGLE_SELECT.Enum(),
			Sections:    protoSections,
		},
	}
}

// BuildNativeFlowButtonsMessage constructs an interactiveMessage with native_flow buttons.
// This is the modern interactive button protocol used by Baileys, Evolution API, etc.
func BuildNativeFlowButtonsMessage(bodyText string, buttons []ButtonOption) *waE2E.Message {
	var nativeButtons []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton
	for _, btn := range buttons {
		paramsJSON := fmt.Sprintf(`{"display_text":%q,"id":%q}`, btn.Text, btn.ID)
		nativeButtons = append(nativeButtons, &waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
			Name:             proto.String("quick_reply"),
			ButtonParamsJSON: proto.String(paramsJSON),
		})
	}

	interactiveMsg := &waE2E.InteractiveMessage{
		Body: &waE2E.InteractiveMessage_Body{
			Text: proto.String(bodyText),
		},
		Footer: &waE2E.InteractiveMessage_Footer{
			Text: proto.String("wabill - WhatsApp Billing"),
		},
		InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{
			NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
				Buttons:        nativeButtons,
				MessageVersion: proto.Int32(1),
			},
		},
	}

	return &waE2E.Message{
		ViewOnceMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{
				MessageContextInfo: &waE2E.MessageContextInfo{
					DeviceListMetadata:        &waE2E.DeviceListMetadata{},
					DeviceListMetadataVersion: proto.Int32(2),
				},
				InteractiveMessage: interactiveMsg,
			},
		},
	}
}

// FormatButtonsTextFallback formats text options nicely when buttons cannot be displayed.
func FormatButtonsTextFallback(bodyText string, buttons []ButtonOption) string {
	var sb strings.Builder
	sb.WriteString(bodyText)
	if len(buttons) > 0 {
		sb.WriteString("\n\n")
		for idx, btn := range buttons {
			sb.WriteString(fmt.Sprintf("%d. %s (Ketik: %s)\n", idx+1, btn.Text, btn.ID))
		}
	}
	return strings.TrimSpace(sb.String())
}
