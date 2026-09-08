package paymentprovider

import "context"

type PaymentInstructions struct {
	BankName      string
	AccountNumber string
	AccountName   string
	Amount        int64
	Note          string
}

// PaymentProvider is an abstraction for handling payments (Manual Bank Transfer now, QRIS / Gateways in the future).
type PaymentProvider interface {
	GetInstructions(ctx context.Context, amount int64) (*PaymentInstructions, error)
}
