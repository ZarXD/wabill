package paymentprovider

import (
	"context"
	"wabill/internal/config"
)

type ManualTransferProvider struct {
	cfg *config.Config
}

func NewManualTransferProvider(cfg *config.Config) *ManualTransferProvider {
	return &ManualTransferProvider{cfg: cfg}
}

func (p *ManualTransferProvider) GetInstructions(ctx context.Context, amount int64) (*PaymentInstructions, error) {
	return &PaymentInstructions{
		BankName:      p.cfg.PaymentBankName,
		AccountNumber: p.cfg.PaymentAccountNumber,
		AccountName:   p.cfg.PaymentAccountName,
		Amount:        amount,
		Note:          "Kirim bukti transfer setelah melakukan pembayaran.",
	}, nil
}
