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
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/repository"
	"github.com/rs/zerolog/log"
)

var ErrConcurrencyConflict = errors.New("concurrency conflict: bill was modified by another request")

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
	getUsersResponse, err := client.UserInternalServiceClient.GetUsers(ctx, connect.NewRequest(
		&userv1.GetUsersRequest{
			UserIds: []int64{paymentBill.PayeeID, paymentBill.PayerID},
		}),
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
	bill := &paymentv1.Bill{
		Id: billId,
		Payee: &userv1.UserInfo{
			Id:        getUsersResponse.Msg.Users[0].Id,
			Name:      getUsersResponse.Msg.Users[0].Name,
			AvatarUrl: getUsersResponse.Msg.Users[0].AvatarUrl,
		},
		Payer: &userv1.UserInfo{
			Id:        getUsersResponse.Msg.Users[1].Id,
			Name:      getUsersResponse.Msg.Users[1].Name,
			AvatarUrl: getUsersResponse.Msg.Users[1].AvatarUrl,
		},
	}

	if err != nil {
		return nil, err
	}

	return bill, nil
}
