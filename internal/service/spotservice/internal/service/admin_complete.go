package service

import (
	"context"
	"strconv"

	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"

	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/adminflow"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"github.com/uptrace/bun"
)

func spotAdminOrderActions(order *model.SpotOrder) []string {
	actions := spotCorrectionActions(order)
	if order.AdminOperationID == "" &&
		(order.Status == model.SpotOrderStatusPaid || order.Status == model.SpotOrderStatusPendingPayment) {
		actions = append(actions, "complete")
	}
	return actions
}

func completeAdminSpotOrder(ctx context.Context, tx bun.Tx, id int64) error {
	order, goods, err := lockOrderAndGoods(ctx, tx, id)
	if err != nil {
		return err
	}
	if order.AdminOperationID != "" ||
		(order.Status != model.SpotOrderStatusPaid && order.Status != model.SpotOrderStatusPendingPayment) {
		return adminflow.Failure(connect.CodeFailedPrecondition, "当前订单状态不允许确认履约")
	}
	bills, err := getSpotCorrectionBills(ctx, "spot_order", order.ID, order.LifecycleRound)
	if err != nil {
		return err
	}
	if err := validateAdminSpotCompletionBills(order, goods, bills); err != nil {
		return err
	}
	_, err = tx.NewUpdate().Model(order).
		Set("status = ?", model.SpotOrderStatusCompleted).
		Set("completed_at = clock_timestamp()").
		Set("paid_at = CASE WHEN payment_bill_id IS NULL THEN paid_at ELSE COALESCE(paid_at,clock_timestamp()) END").
		Set("updated_at = clock_timestamp()").WherePK().Exec(ctx)
	return err
}

func validateAdminSpotCompletionBills(order *model.SpotOrder, goods *model.SpotGoods, bills []*av.SourceBill) error {
	if order.PurchaserID != goods.SellerID {
		if len(bills) != 1 || order.PaymentBillID == nil {
			return adminflow.Failure(connect.CodeFailedPrecondition, "订单缺少有效付款账单，请先核实")
		}
		bill := bills[0]
		if bill.Id != strconv.FormatInt(*order.PaymentBillID, 10) || bill.Status != "completed" ||
			bill.RefundRegistered ||
			bill.GetConfirmationReversed() ||
			bill.Superseded {
			return adminflow.Failure(connect.CodeFailedPrecondition, "订单尚无有效到账款项，不能确认履约")
		}
		snapshot := &spotCorrectionSnapshot{Order: *order, Restored: *order, Goods: *goods}
		if err := validateSpotSourceBill(snapshot, bill); err != nil {
			return err
		}
	} else if len(bills) > 0 {
		return adminflow.Failure(connect.CodeFailedPrecondition, "自购订单关联了账单，请先核实")
	}
	return nil
}
