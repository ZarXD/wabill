package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppEnv                      string
	AppTimezone                 *time.Location
	TimezoneName                string
	DatabaseURL                 string
	InvoiceExpirationMinutes    int
	ReminderDaysBeforeExp       int
	PaymentBankName             string
	PaymentAccountNumber        string
	PaymentAccountName          string
	ProofStoragePath            string
	MaxProofSizeMB              int64
	AdminJIDs                   []string
	EnableNativeButtons         bool
}

// Load loads configuration from environment variables and optionally a .env file.
func Load() (*Config, error) {
	loadDotEnv(".env")

	tzName := getEnv("APP_TIMEZONE", "Asia/Jakarta")
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		loc = time.FixedZone("WIB", 7*3600)
	}

	invExpMin := getEnvAsInt("INVOICE_EXPIRATION_MINUTES", 15)
	reminderDays := getEnvAsInt("REMINDER_DAYS_BEFORE_EXPIRATION", 3)
	maxProofMB := int64(getEnvAsInt("MAX_PROOF_SIZE_MB", 10))
	enableButtons := getEnvAsBool("ENABLE_NATIVE_BUTTONS", true)

	rawAdmins := getEnv("ADMIN_JIDS", "")
	var adminJIDs []string
	if rawAdmins != "" {
		for _, jid := range strings.Split(rawAdmins, ",") {
			trimmed := strings.TrimSpace(jid)
			if trimmed != "" {
				adminJIDs = append(adminJIDs, trimmed)
			}
		}
	}

	cfg := &Config{
		AppEnv:                   getEnv("APP_ENV", "development"),
		AppTimezone:              loc,
		TimezoneName:             tzName,
		DatabaseURL:              getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/wabill?sslmode=disable"),
		InvoiceExpirationMinutes: invExpMin,
		ReminderDaysBeforeExp:    reminderDays,
		PaymentBankName:          getEnv("PAYMENT_BANK_NAME", "BCA"),
		PaymentAccountNumber:     getEnv("PAYMENT_ACCOUNT_NUMBER", "1234567890"),
		PaymentAccountName:       getEnv("PAYMENT_ACCOUNT_NAME", "Fahad"),
		ProofStoragePath:         getEnv("PROOF_STORAGE_PATH", "./data/payment-proofs"),
		MaxProofSizeMB:           maxProofMB,
		AdminJIDs:                adminJIDs,
		EnableNativeButtons:      enableButtons,
	}

	return cfg, nil
}

func (c *Config) IsAdmin(identifiers ...string) bool {
	for _, id := range identifiers {
		if id == "" {
			continue
		}
		cleanPhone := NormalizePhone(id)
		normJID := normalizeJIDString(id)

		for _, admin := range c.AdminJIDs {
			cleanAdminPhone := NormalizePhone(admin)
			normAdminJID := normalizeJIDString(admin)

			if normJID != "" && normJID == normAdminJID {
				return true
			}
			if cleanPhone != "" && cleanPhone == cleanAdminPhone {
				return true
			}
		}
	}
	return false
}

// NormalizePhone normalizes phone numbers into 628... digit format.
func NormalizePhone(s string) string {
	// If it's a JID, take the user part
	if atIdx := strings.Index(s, "@"); atIdx != -1 {
		s = s[:atIdx]
	}
	if colonIdx := strings.Index(s, ":"); colonIdx != -1 {
		s = s[:colonIdx]
	}

	// Keep only digits
	var digits strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	d := digits.String()
	if strings.HasPrefix(d, "0") {
		d = "62" + d[1:]
	} else if strings.HasPrefix(d, "8") {
		d = "62" + d
	}
	return d
}

func normalizeJIDString(jid string) string {
	parts := strings.Split(jid, "@")
	if len(parts) != 2 {
		return strings.TrimSpace(jid)
	}
	user := parts[0]
	server := parts[1]
	if colonIdx := strings.Index(user, ":"); colonIdx != -1 {
		user = user[:colonIdx]
	}
	if dotIdx := strings.Index(user, "."); dotIdx != -1 {
		user = user[:dotIdx]
	}
	return fmtJID(user, server)
}

func fmtJID(user, server string) string {
	return strings.TrimSpace(user) + "@" + strings.TrimSpace(server)
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

func getEnvAsInt(key string, defaultVal int) int {
	valStr := getEnv(key, "")
	if val, err := strconv.Atoi(valStr); err == nil {
		return val
	}
	return defaultVal
}

func getEnvAsBool(key string, defaultVal bool) bool {
	valStr := getEnv(key, "")
	if valStr == "" {
		return defaultVal
	}
	valLower := strings.ToLower(valStr)
	return valLower == "true" || valLower == "1" || valLower == "yes"
}

func loadDotEnv(filepath string) {
	file, err := os.Open(filepath)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, `"'`)
			if _, exists := os.LookupEnv(key); !exists {
				os.Setenv(key, val)
			}
		}
	}
}
