package service

import (
	"context"
	"errors"
	"strings"

	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/client"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/repository"
	"github.com/rs/zerolog/log"
)

var (
	ErrInvalidBuyerErrandOrderContactRequest = errors.New("invalid buyer errand order contact request")
	ErrBuyerErrandOrderNotFound              = errors.New("buyer errand order not found")
	ErrBuyerErrandOrderPermissionDenied      = errors.New("permission denied for buyer errand order")
	ErrBuyerErrandOrderCaptainUnavailable    = errors.New("buyer errand order captain unavailable")
)

type buyerErrandOrderContactDependencies struct {
	getOrderContactRecord func(context.Context, int64) (*repository.BuyerErrandOrderContactRecord, error)
	resolveContactOpenID  func(context.Context, int64) (string, error)
}

var defaultBuyerErrandOrderContactDependencies = buyerErrandOrderContactDependencies{
	getOrderContactRecord: repository.GetBuyerErrandOrderContactRecord,
	resolveContactOpenID:  resolveUserContactOpenID,
}

func GetBuyerErrandOrderCaptainContact(
	ctx context.Context,
	userID int64,
	req *errandv1.GetBuyerErrandOrderCaptainContactRequest,
) (string, error) {
	return getBuyerErrandOrderCaptainContact(ctx, userID, req, defaultBuyerErrandOrderContactDependencies)
}

func getBuyerErrandOrderCaptainContact(
	ctx context.Context,
	userID int64,
	req *errandv1.GetBuyerErrandOrderCaptainContactRequest,
	deps buyerErrandOrderContactDependencies,
) (string, error) {
	if userID <= 0 || req == nil || req.ErrandDemandId <= 0 {
		return "", connect.NewError(connect.CodeInvalidArgument, ErrInvalidBuyerErrandOrderContactRequest)
	}

	record, err := deps.getOrderContactRecord(ctx, req.ErrandDemandId)
	if errors.Is(err, repository.ErrNotFound) {
		return "", connect.NewError(connect.CodeNotFound, ErrBuyerErrandOrderNotFound)
	}
	if err != nil {
		log.Error().
			Err(err).
			Int64("errand_demand_id", req.ErrandDemandId).
			Msg("failed to resolve buyer errand order contact")
		return "", errandInternalError()
	}
	if record.RequesterID != userID {
		return "", connect.NewError(connect.CodePermissionDenied, ErrBuyerErrandOrderPermissionDenied)
	}
	if record.CaptainID == nil || *record.CaptainID <= 0 {
		return "", connect.NewError(connect.CodeFailedPrecondition, ErrBuyerErrandOrderCaptainUnavailable)
	}

	openID, err := deps.resolveContactOpenID(ctx, *record.CaptainID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(openID) == "" {
		return "", connect.NewError(connect.CodeFailedPrecondition, ErrBuyerErrandOrderCaptainUnavailable)
	}
	return openID, nil
}

func resolveUserContactOpenID(ctx context.Context, userID int64) (string, error) {
	response, err := client.UserInternalServiceClient.GetUserContactOpenID(
		ctx,
		connect.NewRequest(&userv1.GetUserContactOpenIDRequest{UserId: userID}),
	)
	if err != nil {
		log.Error().Err(err).Int64("user_id", userID).Msg("failed to resolve user contact")
		return "", errandInternalError()
	}
	return response.Msg.FeishuOpenId, nil
}

func errandInternalError() error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_ErrandError{
		ErrandError: &errandv1.ErrandError{
			Code: errandv1.ErrandErrorCode_ERRAND_ERROR_CODE_INTERNAL_ERROR,
		},
	}, "")
}
