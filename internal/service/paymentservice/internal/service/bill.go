package service

import (
	"context"
	"errors"

	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
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

func GetBill(ctx context.Context, billId int64) (*paymentv1.Bill, error) {
	paymentBill, err := repository.GetBillByID(ctx, billId)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get bill for billId: %d", billId)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_UserError{
			UserError: &userv1.UserError{
				Code: userv1.UserErrorCode_USER_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	getUsersResponse, err := client.UserInternalServiceClient.GetUsers(
		ctx, connect.NewRequest(
			&userv1.GetUsersRequest{
				UserIds: []int64{paymentBill.PayeeID, paymentBill.PayerID},
			},
		),
	)
	if err != nil || len(getUsersResponse.Msg.Users) < 2 {
		log.Error().Err(err).Msgf("Failed to get user info for billId: %d", billId)
		// TODO: return just the bill info without user info instead of returning error.
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_UserError{
			UserError: &userv1.UserError{
				Code: userv1.UserErrorCode_USER_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	// TODO: get the rest of the bill info.
	userByID := make(map[int64]*userv1.UserInfo, len(getUsersResponse.Msg.Users))
	for _, u := range getUsersResponse.Msg.Users {
		userByID[u.Id] = u
	}

	return &paymentv1.Bill{
		Id:    billId,
		Payee: userByID[paymentBill.PayeeID],
		Payer: userByID[paymentBill.PayerID],
	}, nil
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

	getUsersResp, err := client.UserInternalServiceClient.GetUsers(
		ctx, connect.NewRequest(
			&userv1.GetUsersRequest{
				UserIds: []int64{bill.PayerID, bill.PayeeID},
			},
		),
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
