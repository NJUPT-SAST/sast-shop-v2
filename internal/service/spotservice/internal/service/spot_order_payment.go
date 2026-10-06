package service

import (
	"context"

	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/client"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/repository"
	"github.com/uptrace/bun"
)

const spotPaymentSyncBatchSize = 100

func syncPendingSpotOrderPayments(ctx context.Context, userID, storeID int64, perspective string) error {
	var afterID int64
	for {
		records, err := repository.ListPendingSpotOrderPayments(
			ctx,
			userID,
			storeID,
			perspective,
			afterID,
			spotPaymentSyncBatchSize,
		)
		if err != nil {
			return spotInternalError()
		}
		if len(records) == 0 {
			return nil
		}
		billIDs := make([]int64, 0, len(records))
		for _, record := range records {
			billIDs = append(billIDs, *record.PaymentBillID)
		}
		if client.PaymentInternalServiceClient == nil {
			return spotInternalError()
		}
		response, err := client.PaymentInternalServiceClient.BatchGetBills(
			ctx,
			connect.NewRequest(&paymentv1.BatchGetBillsRequest{BillIds: billIDs}),
		)
		if err != nil {
			return err
		}
		bills := make(map[int64]*paymentv1.Bill, len(response.Msg.Bills))
		for _, bill := range response.Msg.Bills {
			if bill != nil {
				bills[bill.Id] = bill
			}
		}
		for _, record := range records {
			if err := syncSpotOrderPayment(
				ctx,
				postgres.DB,
				record.ID,
				record.PaymentBillID,
				record.TotalAmountCents,
				bills[*record.PaymentBillID],
			); err != nil {
				return err
			}
		}
		afterID = records[len(records)-1].ID
		if len(records) < spotPaymentSyncBatchSize {
			return nil
		}
	}
}

func validateSpotOrderBill(orderID int64, billID *int64, amountCents int32, bill *paymentv1.Bill) error {
	if billID == nil || bill == nil || bill.Id != *billID || bill.GetSourceType() != "spot_order" ||
		bill.GetSourceId() != orderID || bill.AmountCents != amountCents {
		return connect.NewError(connect.CodeFailedPrecondition, ErrInvalidSpotOrderStatus)
	}
	return nil
}

func syncSpotOrderPayment(
	ctx context.Context,
	db bun.IDB,
	orderID int64,
	billID *int64,
	amountCents int32,
	bill *paymentv1.Bill,
) error {
	if err := validateSpotOrderBill(orderID, billID, amountCents, bill); err != nil {
		return err
	}
	if bill.Status != paymentv1.BillStatus_BILL_STATUS_COMPLETED {
		return nil
	}
	if bill.CompletedAt == nil || !bill.CompletedAt.IsValid() {
		return spotInternalError()
	}
	if err := repository.MarkSpotOrderPaid(ctx, db, orderID, *billID, bill.CompletedAt.AsTime()); err != nil {
		return spotInternalError()
	}
	return nil
}

func ensureSpotOrderPaymentCompleted(ctx context.Context, tx bun.Tx, order *model.SpotOrder) error {
	if order.Status != model.SpotOrderStatusPendingPayment && order.Status != model.SpotOrderStatusPaid {
		return connect.NewError(connect.CodeFailedPrecondition, ErrInvalidSpotOrderStatus)
	}
	bill, err := getBill(ctx, order.PaymentBillID)
	if err != nil {
		return err
	}
	if err := validateSpotOrderBill(order.ID, order.PaymentBillID, order.TotalAmountCents, bill); err != nil {
		return err
	}
	if bill.Status != paymentv1.BillStatus_BILL_STATUS_COMPLETED {
		return connect.NewError(connect.CodeFailedPrecondition, ErrInvalidSpotOrderStatus)
	}
	return syncSpotOrderPayment(ctx, tx, order.ID, order.PaymentBillID, order.TotalAmountCents, bill)
}
