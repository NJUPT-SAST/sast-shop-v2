package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/model"
	"github.com/uptrace/bun"
)

var ErrWestPocketPaymentStarted = errors.New("a West Pocket payment has already been submitted")

// WithWestPocketSourceLock serializes creation and cancellation, including members
// whose bills have not been inserted yet. Authorization must run inside fn after
// acquiring this lock so a delayed publisher cannot insert after cancellation.
func WithWestPocketSourceLock(ctx context.Context, pocketID int64, fn func(context.Context, bun.Tx) error) error {
	return postgres.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		key := fmt.Sprintf("west_pocket:%d", pocketID)
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", key); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}

func GetWestPocketBill(ctx context.Context, db bun.IDB, pocketID, payerID int64) (*model.PaymentBill, error) {
	var bill model.PaymentBill
	err := db.NewSelect().Model(&bill).
		Where("source_type = ? AND source_id = ? AND payer_id = ?", "west_pocket", pocketID, payerID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &bill, err
}

// CancelWestPocketBillsIfUnpaid is all-or-nothing: submitted is already a
// payment claim and cannot be silently discarded along with the unpaid bills.
func CancelWestPocketBillsIfUnpaid(ctx context.Context, pocketID int64, authorize func(context.Context) error) error {
	return WithWestPocketSourceLock(ctx, pocketID, func(ctx context.Context, tx bun.Tx) error {
		if err := authorize(ctx); err != nil {
			return err
		}
		var bills []model.PaymentBill
		if err := tx.NewSelect().Model(&bills).
			Where("source_type = ? AND source_id = ?", "west_pocket", pocketID).
			OrderExpr("id ASC").For("UPDATE").Scan(ctx); err != nil {
			return err
		}
		for _, bill := range bills {
			if bill.Status != model.PaymentBillStatusUnpaid && bill.Status != model.PaymentBillStatusClosed {
				return ErrWestPocketPaymentStarted
			}
		}
		_, err := cancelBillsBySource(ctx, tx, "west_pocket", pocketID, nil)
		return err
	})
}
