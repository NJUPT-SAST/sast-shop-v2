package repository

import (
	"context"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/model"
	"github.com/uptrace/bun"
)

func GetBillByID(ctx context.Context, billID int64) (*model.PaymentBill, error) {
	var bill model.PaymentBill
	err := postgres.DB.NewSelect().Model(&bill).Where("id = ?", billID).Scan(ctx)
	return &bill, err
}

func CreateBill(ctx context.Context, bill *model.PaymentBill) error {
	_, err := postgres.DB.NewInsert().Model(bill).Exec(ctx)
	return err
}

func GetBillBySource(ctx context.Context, sourceType string, sourceID int64, payerID int64) (*model.PaymentBill, error) {
	var bill model.PaymentBill
	err := postgres.DB.NewSelect().Model(&bill).Where("source_type = ? AND source_id = ? AND payer_id = ? AND status != ?", sourceType, sourceID, payerID, model.PaymentBillStatusClosed).Scan(ctx)
	return &bill, err
}

func UpdateBillStatus(ctx context.Context,
	billID int64,
	expectedUpdatedAt time.Time,
	newStatus model.PaymentBillStatus,
	extraUpdates map[string]any) (int64, error) {
	res, err := postgres.DB.NewUpdate().Model(extraUpdates).TableExpr("payment.payment_bill").Set("status = ?", newStatus).
		Set("updated_at = ?", time.Now()).Where("id = ? AND updated_at = ?", billID, expectedUpdatedAt).Exec(ctx)
	if err != nil {
		return 0, err
	}
	affected, _ := res.RowsAffected()
	return affected, err
}

func CancelBillsBySource(ctx context.Context, sourceType string, sourceID int64, payerID *int64) (int64, error) {
	q := postgres.DB.NewUpdate().
		Model((*model.PaymentBill)(nil)).
		Set("status = ?", model.PaymentBillStatusClosed).
		Set("closed_at = ?", time.Now()).
		Where("source_type = ?", sourceType).
		Where("source_id = ?", sourceID).
		Where("status IN (?)", bun.List([]model.PaymentBillStatus{
			model.PaymentBillStatusUnpaid,
			model.PaymentBillStatusSubmitted,
		}))

	if payerID != nil {
		q = q.Where("payer_id = ?", *payerID)
	}

	res, err := q.Exec(ctx)
	if err != nil {
		return 0, err
	}
	affected, _ := res.RowsAffected()
	return affected, nil
}

func CreateConfirmationLog(ctx context.Context, log *model.PaymentConfirmationLog) error {
	_, err := postgres.DB.NewInsert().Model(log).Exec(ctx)
	return err
}
