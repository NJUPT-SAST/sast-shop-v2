package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"github.com/uptrace/bun"
)

// ListPendingSpotOrderPayments scans the whole visible pending set by ID before
// the caller applies a status filter or page offset.
func ListPendingSpotOrderPayments(
	ctx context.Context,
	userID, storeID int64,
	perspective string,
	afterID int64,
	limit int,
) ([]SpotOrderRecord, error) {
	query := spotOrderRecordBaseQuery().
		Where("sg.store_id = ?", storeID).
		Where("so.status = ?", model.SpotOrderStatusPendingPayment).
		Where("so.payment_bill_id IS NOT NULL").
		Where("so.id > ?", afterID).
		OrderExpr("so.id ASC").
		Limit(limit)
	switch perspective {
	case "purchaser":
		query = query.Where("so.purchaser_id = ?", userID)
	case "seller":
		query = query.Where("sg.seller_id = ?", userID)
	default:
		return nil, fmt.Errorf("invalid perspective")
	}
	var records []SpotOrderRecord
	err := query.Scan(ctx, &records)
	return records, err
}

// MarkSpotOrderPaid never changes a cancelled or completed order, even when the
// payment snapshot was loaded before a concurrent order transition.
func MarkSpotOrderPaid(ctx context.Context, db bun.IDB, orderID, billID int64, paidAt time.Time) error {
	_, err := db.NewUpdate().Model((*model.SpotOrder)(nil)).
		Set("status = ?", model.SpotOrderStatusPaid).
		Set("paid_at = ?", paidAt).
		Set("updated_at = clock_timestamp()").
		Where("id = ?", orderID).
		Where("payment_bill_id = ?", billID).
		Where("status = ?", model.SpotOrderStatusPendingPayment).
		Exec(ctx)
	return err
}
