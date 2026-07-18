package service

import (
	"context"
	"errors"
	"strings"

	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/client"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/repository"
	"github.com/rs/zerolog/log"
)

var ErrSpotOrderSellerContactUnavailable = errors.New("spot order seller contact unavailable")

type spotOrderContactDependencies struct {
	getOrderRecord       func(context.Context, int64) (*repository.SpotOrderRecord, error)
	resolveContactOpenID func(context.Context, int64) (string, error)
}

var defaultSpotOrderContactDependencies = spotOrderContactDependencies{
	getOrderRecord:       repository.GetSpotOrderRecord,
	resolveContactOpenID: resolveUserContactOpenID,
}

func GetSpotOrderSellerContact(
	ctx context.Context,
	userID int64,
	req *spotv1.GetSpotOrderSellerContactRequest,
) (string, error) {
	return getSpotOrderSellerContact(ctx, userID, req, defaultSpotOrderContactDependencies)
}

func getSpotOrderSellerContact(
	ctx context.Context,
	userID int64,
	req *spotv1.GetSpotOrderSellerContactRequest,
	deps spotOrderContactDependencies,
) (string, error) {
	if userID <= 0 || req == nil || req.SpotOrderId <= 0 {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("invalid spot order seller contact request"))
	}

	record, err := deps.getOrderRecord(ctx, req.SpotOrderId)
	if errors.Is(err, repository.ErrNotFound) {
		return "", connect.NewError(connect.CodeNotFound, ErrSpotOrderNotFound)
	}
	if err != nil {
		log.Error().Err(err).Int64("spot_order_id", req.SpotOrderId).Msg("failed to resolve spot order contact")
		return "", spotInternalError()
	}
	if record.PurchaserID != userID {
		return "", connect.NewError(connect.CodePermissionDenied, ErrSpotOrderPermissionDenied)
	}

	openID, err := deps.resolveContactOpenID(ctx, record.SellerID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(openID) == "" {
		return "", connect.NewError(connect.CodeFailedPrecondition, ErrSpotOrderSellerContactUnavailable)
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
		return "", spotInternalError()
	}
	return response.Msg.FeishuOpenId, nil
}
