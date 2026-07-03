package repository

import (
	"context"
	"database/sql"
	"errors"
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

func GetBillBySource(
	ctx context.Context,
	sourceType string,
	sourceID int64,
	payerID int64,
) (*model.PaymentBill, error) {
	var bill model.PaymentBill
	err := postgres.DB.NewSelect().
		Model(&bill).
		Where("source_type = ? AND source_id = ? AND payer_id = ? AND status != ?", sourceType, sourceID, payerID, model.PaymentBillStatusClosed).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &bill, nil
}

func UpdateBillStatus(ctx context.Context,
	billID int64,
	expectedUpdatedAt time.Time,
	newStatus model.PaymentBillStatus,
	extraUpdates map[string]any,
) (int64, error) {
	if extraUpdates == nil {
		extraUpdates = make(map[string]any)
	}
	// 防止调用方误传保留字段导致 SQL 中重复 SET / 或意外更新主键
	delete(extraUpdates, "id")
	delete(extraUpdates, "status")
	delete(extraUpdates, "updated_at")

	now := time.Now()
	res, err := postgres.DB.NewUpdate().
		Model(&extraUpdates).
		TableExpr("payment.payment_bill").
		Set("status = ?", newStatus).
		Set("updated_at = ?", now).
		Where("id = ? AND updated_at = ?", billID, expectedUpdatedAt).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return affected, nil
}

func CancelBillBySource(ctx context.Context, sourceType string, sourceID int64, payerID *int64) (int64, error) {
	now := time.Now()
	q := postgres.DB.NewUpdate().
		Model((*model.PaymentBill)(nil)).
		Set("status = ?", model.PaymentBillStatusClosed).
		Set("closed_at = ?", now).
		Set("updated_at = ?", now).
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
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return affected, nil
}

func CreateConfirmationLog(ctx context.Context, log *model.PaymentConfirmationLog) error {
	_, err := postgres.DB.NewInsert().Model(log).Exec(ctx)
	return err
}
