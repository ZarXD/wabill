package config_test

import (
	"testing"
	"wabill/internal/config"
)

func TestAdminAuthorization(t *testing.T) {
	cfg := &config.Config{
		AdminJIDs: []string{
			"628123456789@s.whatsapp.net",
			"628987654321@s.whatsapp.net",
		},
	}

	tests := []struct {
		name      string
		senderJID string
		wantAdmin bool
	}{
		{"Exact match admin 1", "628123456789@s.whatsapp.net", true},
		{"Admin with device suffix :1", "628123456789:1@s.whatsapp.net", true},
		{"Admin with device suffix :5", "628987654321:5@s.whatsapp.net", true},
		{"Admin with dot agent suffix", "628123456789.0:1@s.whatsapp.net", true},
		{"Admin with Indonesian local format 08123456789", "08123456789", true},
		{"Admin with international plus +628123456789", "+628123456789", true},
		{"Unauthorized user", "628555555555@s.whatsapp.net", false},
		{"Empty JID", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.IsAdmin(tt.senderJID)
			if got != tt.wantAdmin {
				t.Errorf("IsAdmin(%q) = %v, want %v", tt.senderJID, got, tt.wantAdmin)
			}
		})
	}
}
