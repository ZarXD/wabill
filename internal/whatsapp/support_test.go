package whatsapp_test

import (
	"testing"
	"time"

	"wabill/internal/whatsapp"
)

func TestSupportSessionManager(t *testing.T) {
	mgr := whatsapp.NewSupportSessionManager(500 * time.Millisecond)

	phone := "628123456789"
	jid := "628123456789@s.whatsapp.net"
	name := "Fahad"

	// Initially no session
	if mgr.HasActiveSession(phone) {
		t.Fatalf("Expected no active session initially")
	}

	// Open session
	s := mgr.OpenSession(phone, jid, name)
	if s == nil || s.PhoneNumber != phone || s.PushName != name {
		t.Fatalf("Failed to open session properly: %+v", s)
	}

	// Verify active
	if !mgr.HasActiveSession(phone) {
		t.Fatalf("Expected active session")
	}

	retrieved := mgr.GetSession(phone)
	if retrieved == nil || retrieved.CustomerJID != jid {
		t.Fatalf("Failed to retrieve active session")
	}

	// Close session
	closed := mgr.CloseSession(phone)
	if !closed {
		t.Fatalf("Expected CloseSession to return true")
	}

	if mgr.HasActiveSession(phone) {
		t.Fatalf("Expected session to be closed")
	}

	// Test timeout
	mgr.OpenSession(phone, jid, name)
	// Test ShouldSendFeedback throttling
	if !mgr.ShouldSendFeedback(phone) {
		t.Fatalf("Expected ShouldSendFeedback to be true on first call")
	}
	if mgr.ShouldSendFeedback(phone) {
		t.Fatalf("Expected ShouldSendFeedback to be false on immediate second call")
	}

	time.Sleep(600 * time.Millisecond)
	if mgr.HasActiveSession(phone) {
		t.Fatalf("Expected session to timeout after 500ms")
	}
}
