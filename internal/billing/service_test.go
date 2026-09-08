package billing_test

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"testing"

	"wabill/internal/billing"
	"wabill/internal/config"
	"wabill/internal/domain"
	"wabill/internal/paymentprovider"
)

func createSampleJPEG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, nil)
	return buf.Bytes()
}

func TestSubmitPaymentProofValidation(t *testing.T) {
	cfg := &config.Config{
		MaxProofSizeMB: 1, // 1 MB
	}
	svc := billing.NewService(cfg, nil, nil, nil, nil, nil, nil, &paymentprovider.ManualTransferProvider{})

	ctx := context.Background()

	// Case 1: File too large
	oversizedBytes := make([]byte, 2*1024*1024) // 2 MB
	_, _, err := svc.SubmitPaymentProof(ctx, "628123@s.whatsapp.net", "628123", oversizedBytes)
	if err != domain.ErrFileTooLarge {
		t.Errorf("expected ErrFileTooLarge, got: %v", err)
	}

	// Case 2: Invalid MIME type (text instead of image)
	invalidTextBytes := []byte("this is plain text not an image")
	_, _, err = svc.SubmitPaymentProof(ctx, "628123@s.whatsapp.net", "628123", invalidTextBytes)
	if err != domain.ErrInvalidProofMedia {
		t.Errorf("expected ErrInvalidProofMedia, got: %v", err)
	}
}
