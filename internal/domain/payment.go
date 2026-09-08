package domain

import "time"

type Payment struct {
	ID            int64     `json:"id"`
	InvoiceID     int64     `json:"invoice_id"`
	Amount        int64     `json:"amount"`
	PaymentMethod string    `json:"payment_method"`
	PaidAt        time.Time `json:"paid_at"`
	ApprovedBy    string    `json:"approved_by"`
	CreatedAt     time.Time `json:"created_at"`
}

type PaymentProof struct {
	ID         int64     `json:"id"`
	InvoiceID  int64     `json:"invoice_id"`
	FilePath   string    `json:"file_path"`
	FileSize   int64     `json:"file_size"`
	MimeType   string    `json:"mime_type"`
	UploadedAt time.Time `json:"uploaded_at"`
}
