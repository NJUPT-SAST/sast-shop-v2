package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/client"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/repository"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrConcurrencyConflict = errors.New("concurrency conflict: bill was modified by another request")
	ErrBillNotFound        = errors.New("bill not found")
	ErrInvalidBillStatus   = errors.New("invalid bill status")
	ErrInvalidChannel      = errors.New("invalid channel")
	ErrDuplicateBill       = errors.New("duplicate bill")
)

func CreateBill(ctx context.Context, payerId, payeeId int64, amountCents int32, sourceType *string, sourceId *int64) (*paymentv1.Bill, error) {
	if payerId == payeeId {
		return nil, fmt.Errorf("create bill: payer and payee cannot be the same user")
	}

	bill := &model.PaymentBill{
		BillNo:      model.GenerateBillNo(),
		PayerID:     payerId,
		PayeeID:     payeeId,
		SourceType:  sourceType,
		SourceID:    sourceId,
		AmountCents: amountCents,
		VerifyCode:  model.GenerateVerifyCode(),
		Status:      model.PaymentBillStatusUnpaid,
	}
	err := repository.CreateBill(ctx, bill)
	if err != nil {
		log.Error().Err(err).Msg("CreateBill: CreateBill failed")
		return nil, fmt.Errorf("create bill: %w", err)
	}
	return PaymentBillToProto(ctx, bill)
}

func GetBill(ctx context.Context, billId int64) (*paymentv1.Bill, error) {
	paymentBill, err := repository.GetBillByID(ctx, billId)
	if err != nil {
		log.Error().Err(err).Msgf("GetBill: GetBillByID failed for billId: %d", billId)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrBillNotFound
		}
		return nil, fmt.Errorf("get bill: %w", err)
	}
	return PaymentBillToProto(ctx, paymentBill)
}

func PayBill(ctx context.Context, billId int64, channel paymentv1.Channel, expectedUpdatedAt time.Time) (*paymentv1.Bill, error) {
	bill, err := repository.GetBillByID(ctx, billId)
	if err != nil {
		log.Error().Err(err).Msgf("PayBill: GetBillByID failed for billId: %d", billId)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrBillNotFound
		}
		return nil, fmt.Errorf("pay bill: %w", err)
	}

	if bill.Status != model.PaymentBillStatusUnpaid {
		return nil, ErrInvalidBillStatus
	}

	ch, ok := model.ProtoChannelToModel(channel)
	if !ok {
		return nil, ErrInvalidChannel
	}

	now := time.Now()
	affected, err := repository.UpdateBillStatus(ctx, billId, expectedUpdatedAt, model.PaymentBillStatusSubmitted, map[string]any{
		"channel":      ch,
		"submitted_at": now,
	})
	if err != nil {
		log.Error().Err(err).Msgf("PayBill: UpdateBillStatus failed for billId: %d", billId)
		return nil, fmt.Errorf("pay bill: %w", err)
	}
	if affected == 0 {
		return nil, ErrConcurrencyConflict
	}

	bill.Status = model.PaymentBillStatusSubmitted
	bill.Channel = &ch
	bill.SubmittedAt = &now
	bill.UpdatedAt = now

	return PaymentBillToProto(ctx, bill)
}

func ConfirmBill(ctx context.Context, billId int64, expectedUpdatedAt time.Time) (*paymentv1.Bill, error) {
	bill, err := repository.GetBillByID(ctx, billId)
	if err != nil {
		log.Error().Err(err).Msgf("ConfirmBill: GetBillByID failed for billId: %d", billId)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrBillNotFound
		}
		return nil, fmt.Errorf("confirm bill: %w", err)
	}

	if bill.Status != model.PaymentBillStatusSubmitted {
		return nil, ErrInvalidBillStatus
	}

	now := time.Now()
	affected, err := repository.UpdateBillStatus(ctx, billId, expectedUpdatedAt, model.PaymentBillStatusCompleted, map[string]any{
		"completed_at": now,
	})
	if err != nil {
		log.Error().Err(err).Msgf("ConfirmBill: UpdateBillStatus failed for billId: %d", billId)
		return nil, fmt.Errorf("confirm bill: %w", err)
	}
	if affected == 0 {
		return nil, ErrConcurrencyConflict
	}

	bill.Status = model.PaymentBillStatusCompleted
	bill.CompletedAt = &now
	bill.UpdatedAt = now

	return PaymentBillToProto(ctx, bill)
}

func TransitionBill(ctx context.Context, billId int64, targetStatus paymentv1.BillStatus, expectedUpdatedAt time.Time, operatorID int64) (*paymentv1.Bill, error) {
	bill, err := repository.GetBillByID(ctx, billId)
	if err != nil {
		log.Error().Err(err).Msgf("TransitionBill: GetBillByID failed for billId: %d", billId)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrBillNotFound
		}
		return nil, fmt.Errorf("transition bill: %w", err)
	}

	newStatus, ok := model.ProtoStatusToModel(targetStatus)
	if !ok {
		return nil, ErrInvalidBillStatus
	}

	// 仅允许 submitted→unpaid（打回待支付）
	if bill.Status != model.PaymentBillStatusSubmitted || newStatus != model.PaymentBillStatusUnpaid {
		return nil, ErrInvalidBillStatus
	}

	// 仅收款方可打回
	if operatorID != bill.PayeeID {
		return nil, ErrInvalidBillStatus
	}

	now := time.Now()

	affected, err := repository.UpdateBillStatus(ctx, billId, expectedUpdatedAt, newStatus, map[string]any{
		"submitted_at": nil,
		"channel":      nil,
	})
	if err != nil {
		log.Error().Err(err).Msgf("TransitionBill: UpdateBillStatus failed for billId: %d", billId)
		return nil, fmt.Errorf("transition bill: %w", err)
	}
	if affected == 0 {
		return nil, ErrConcurrencyConflict
	}

	logEntry := &model.PaymentConfirmationLog{
		BillID:     billId,
		OperatorID: operatorID,
		FromStatus: bill.Status,
		ToStatus:   newStatus,
	}
	if logErr := repository.CreateConfirmationLog(ctx, logEntry); logErr != nil {
		log.Error().Err(logErr).Msgf("TransitionBill: CreateConfirmationLog failed for billId: %d", billId)
	}

	bill.Status = newStatus
	bill.Channel = nil
	bill.UpdatedAt = now
	bill.SubmittedAt = nil

	return PaymentBillToProto(ctx, bill)
}

func SupplementSerialNumber(ctx context.Context, billId int64, serialNumber string, expectedUpdatedAt time.Time) (*paymentv1.Bill, error) {
	bill, err := repository.GetBillByID(ctx, billId)
	if err != nil {
		log.Error().Err(err).Msgf("SupplementSerialNumber: GetBillByID failed for billId: %d", billId)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrBillNotFound
		}
		return nil, fmt.Errorf("supplement serial number: %w", err)
	}

	if bill.Status != model.PaymentBillStatusSubmitted {
		return nil, ErrInvalidBillStatus
	}

	affected, err := repository.UpdateBillStatus(ctx, billId, expectedUpdatedAt, bill.Status, map[string]any{
		"serial_number": serialNumber,
	})
	if err != nil {
		log.Error().Err(err).Msgf("SupplementSerialNumber: UpdateBillStatus failed for billId: %d", billId)
		return nil, fmt.Errorf("supplement serial number: %w", err)
	}
	if affected == 0 {
		return nil, ErrConcurrencyConflict
	}

	bill.SerialNumber = serialNumber
	bill.UpdatedAt = time.Now()

	return PaymentBillToProto(ctx, bill)
}

func PaymentBillToProto(ctx context.Context, bill *model.PaymentBill) (*paymentv1.Bill, error) {
	status, ok := model.ModelStatusToProto(bill.Status)
	if !ok {
		return nil, ErrInvalidBillStatus
	}

	pb := &paymentv1.Bill{
		Id:          bill.ID,
		BillNo:      bill.BillNo,
		Status:      status,
		AmountCents: bill.AmountCents,
		VerifyCode:  bill.VerifyCode,
		CreatedAt:   timestamppb.New(bill.CreatedAt),
		UpdatedAt:   timestamppb.New(bill.UpdatedAt),
	}

	if bill.SerialNumber != "" {
		pb.SerialNumber = &bill.SerialNumber
	}
	if bill.SourceType != nil {
		pb.SourceType = bill.SourceType
	}
	if bill.SourceID != nil {
		pb.SourceId = bill.SourceID
	}
	if bill.Channel != nil {
		ch, ok := model.ModelChannelToProto(*bill.Channel)
		if !ok {
			return nil, ErrInvalidChannel
		}
		pb.Channel = ch
	}
	if bill.SubmittedAt != nil {
		pb.SubmittedAt = timestamppb.New(*bill.SubmittedAt)
	}
	if bill.CompletedAt != nil {
		pb.CompletedAt = timestamppb.New(*bill.CompletedAt)
	}
	if bill.ClosedAt != nil {
		pb.ClosedAt = timestamppb.New(*bill.ClosedAt)
	}

	getUsersResp, err := client.UserInternalServiceClient.GetUsers(ctx, connect.NewRequest(
		&userv1.GetUsersRequest{
			UserIds: []int64{bill.PayerID, bill.PayeeID},
		}),
	)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get user info for billId: %d", bill.ID)
		return pb, nil
	}
	userByID := make(map[int64]*userv1.UserInfo, len(getUsersResp.Msg.Users))
	for _, u := range getUsersResp.Msg.Users {
		userByID[u.Id] = u
	}
	pb.Payer = userByID[bill.PayerID]
	pb.Payee = userByID[bill.PayeeID]
	if pb.Payer == nil || pb.Payee == nil {
		log.Error().Msgf("Failed to map user info for billId: %d", bill.ID)
	}

	return pb, nil
}
