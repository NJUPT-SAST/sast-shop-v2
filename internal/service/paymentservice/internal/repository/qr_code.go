package repository

import (
	"context"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/model"
)

// GetQRCodesByOwnerID returns all QR codes owned by a given user.
func GetQRCodesByOwnerID(ctx context.Context, ownerID int64) ([]*model.PaymentQRCode, error) {
	var qrCodes []*model.PaymentQRCode
	err := postgres.DB.NewSelect().
		Model(&qrCodes).
		Where("owner_id = ?", ownerID).
		Order("id ASC").
		Scan(ctx)
	return qrCodes, err
}

// UpsertQRCode inserts or updates a QR code for the given (owner_id, channel) pair.
// The unique index uq_payment_qr_owner_channel enforces one code per owner per channel.
func UpsertQRCode(ctx context.Context, qrCode *model.PaymentQRCode) (*model.PaymentQRCode, error) {
	now := time.Now()
	qrCode.CreatedAt = now
	qrCode.UpdatedAt = now

	_, err := postgres.DB.NewInsert().
		Model(qrCode).
		On("CONFLICT (owner_id, channel) DO UPDATE").
		Set("content = EXCLUDED.content").
		Set("updated_at = EXCLUDED.updated_at").
		Exec(ctx)
	if err != nil {
		return nil, err
	}

	// Fetch the result to obtain server-generated values (id, timestamps).
	var result model.PaymentQRCode
	err = postgres.DB.NewSelect().
		Model(&result).
		Where("owner_id = ? AND channel = ?", qrCode.OwnerID, qrCode.Channel).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
