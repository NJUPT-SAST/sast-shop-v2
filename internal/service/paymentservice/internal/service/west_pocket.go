package service

import (
	"context"
	"errors"
	"os"
	"time"

	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	westpocketv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/westpocket/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/client"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/repository"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const westPocketSource = "west_pocket"

func getWestPocketCollectionState(
	ctx context.Context,
	pocketID int64,
) (*westpocketv1.GetCollectionStateResponse, error) {
	if client.WestPocketInternalServiceClient == nil || os.Getenv("WEST_POCKET_INTERNAL_TOKEN") == "" {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("west pocket service is not configured"))
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req := connect.NewRequest(&westpocketv1.GetCollectionStateRequest{PocketId: pocketID})
	req.Header().Set(interceptor.WestPocketServiceHeader, os.Getenv("WEST_POCKET_INTERNAL_TOKEN"))
	res, err := client.WestPocketInternalServiceClient.GetCollectionState(ctx, req)
	if err != nil {
		return nil, err
	}
	if res == nil || res.Msg == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("empty West Pocket state"))
	}
	return res.Msg, nil
}

func validateWestPocketBill(
	state *westpocketv1.GetCollectionStateResponse,
	bill *model.PaymentBill,
	status string,
) error {
	if state == nil || state.Status != status || bill == nil || bill.SourceType == nil ||
		*bill.SourceType != westPocketSource ||
		bill.SourceID == nil ||
		*bill.SourceID <= 0 {
		return ErrInvalidBillStatus
	}
	if state.OwnerId != bill.PayeeID || bill.PayerID == bill.PayeeID || bill.AmountCents <= 0 {
		return ErrInvalidBillRequest
	}
	for _, member := range state.Members {
		if member.UserId == bill.PayerID && member.ShareCents == bill.AmountCents && !member.IsOwner {
			return nil
		}
	}
	return ErrInvalidBillRequest
}

func requireWestPocketCollecting(ctx context.Context, bill *model.PaymentBill) error {
	if bill.SourceType == nil || *bill.SourceType != westPocketSource {
		return nil
	}
	if bill.SourceID == nil || *bill.SourceID <= 0 {
		return ErrInvalidBillRequest
	}
	state, err := getWestPocketCollectionState(ctx, *bill.SourceID)
	if err != nil {
		return err
	}
	return validateWestPocketBill(state, bill, "collecting")
}

func createWestPocketBill(
	ctx context.Context,
	pocketID, payerID, payeeID int64,
	amountCents int32,
) (*paymentv1.Bill, error) {
	sourceType := westPocketSource
	wanted := &model.PaymentBill{
		PayerID:     payerID,
		PayeeID:     payeeID,
		SourceType:  &sourceType,
		SourceID:    &pocketID,
		AmountCents: amountCents,
	}
	var result *model.PaymentBill
	err := repository.WithWestPocketSourceLock(ctx, pocketID, func(ctx context.Context, tx bun.Tx) error {
		state, err := getWestPocketCollectionState(ctx, pocketID)
		if err != nil {
			return err
		}
		if err := validateWestPocketBill(state, wanted, "publishing"); err != nil {
			return err
		}
		existing, err := repository.GetWestPocketBill(ctx, tx, pocketID, payerID)
		if err != nil {
			return err
		}
		if existing != nil {
			if !sameWestPocketBill(existing, wanted) {
				return ErrDuplicateBill
			}
			if existing.Status == model.PaymentBillStatusClosed {
				return ErrInvalidBillStatus
			}
			result = existing
			return nil
		}
		billNo, err := newPaymentBillNo()
		if err != nil {
			return err
		}
		wanted.BillNo = billNo
		wanted.VerifyCode = model.GenerateVerifyCode()
		wanted.Status = model.PaymentBillStatusUnpaid
		if _, err := tx.NewInsert().Model(wanted).Returning("*").Exec(ctx); err != nil {
			return err
		}
		result = wanted
		return nil
	})
	if err != nil {
		return nil, err
	}
	return PaymentBillToProto(ctx, result)
}

func sameWestPocketBill(a, b *model.PaymentBill) bool {
	return a != nil && b != nil && a.SourceType != nil && b.SourceType != nil &&
		a.SourceID != nil && b.SourceID != nil && *a.SourceType == *b.SourceType && *a.SourceID == *b.SourceID &&
		a.PayerID == b.PayerID && a.PayeeID == b.PayeeID && a.AmountCents == b.AmountCents
}

func CancelWestPocketBillsIfUnpaid(ctx context.Context, pocketID int64) error {
	if pocketID <= 0 {
		return ErrInvalidBillRequest
	}
	err := repository.CancelWestPocketBillsIfUnpaid(ctx, pocketID, func(ctx context.Context) error {
		state, err := getWestPocketCollectionState(ctx, pocketID)
		if err != nil {
			return err
		}
		if state.Status != "cancelling" && state.Status != "cancelled" {
			return ErrInvalidBillStatus
		}
		return nil
	})
	if errors.Is(err, repository.ErrWestPocketPaymentStarted) {
		return ErrInvalidBillStatus
	}
	return err
}

func GetPayeeQrCode(ctx context.Context, ownerID int64) (*paymentv1.GetPayeeQrCodeResponse, error) {
	if ownerID <= 0 {
		return nil, ErrInvalidBillRequest
	}
	codes, err := repository.GetQRCodesByOwnerID(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	for _, code := range codes {
		if code.Channel == model.PaymentChannelWechat && code.Content != "" {
			return &paymentv1.GetPayeeQrCodeResponse{
				Id:        code.ID,
				Content:   code.Content,
				UpdatedAt: timestamppb.New(code.UpdatedAt),
			}, nil
		}
	}
	return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("请先上传微信收款码"))
}
