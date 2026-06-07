package repository

import (
	"context"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/model"
)

func GetBillByID(ctx context.Context, billID int64) (*model.PaymentBill, error) {
	var bill model.PaymentBill
	err := postgres.DB.NewSelect().Model(&bill).Where("id = ?", billID).Scan(ctx)
	return &bill, err
}