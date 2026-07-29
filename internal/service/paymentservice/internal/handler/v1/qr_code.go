package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/payment/v1/paymentv1connect"
	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	"connectrpc.com/connect"
	rpcinterceptor "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/service"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type QrCodeServiceServer struct {
	paymentv1connect.QrCodeServiceHandler
}

func (s *QrCodeServiceServer) GetQrCode(
	ctx context.Context,
	r *connect.Request[paymentv1.GetQrCodeRequest],
) (*connect.Response[paymentv1.GetQrCodeResponse], error) {
	var ownerID int64
	if r.Msg.OwnerId != nil {
		ownerID = *r.Msg.OwnerId
	} else {
		user, ok := rpcinterceptor.UserFromContext(ctx)
		if !ok {
			return nil, paymentError()
		}
		ownerID = user.UserID
	}

	qrCodes, err := service.GetQrCode(ctx, ownerID)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get QR codes for ownerID: %d", ownerID)
		return nil, mapServiceError(err)
	}

	return connect.NewResponse(&paymentv1.GetQrCodeResponse{
		QrCodes: qrCodes,
	}), nil
}

func (s *QrCodeServiceServer) UpdateQrCode(
	ctx context.Context,
	r *connect.Request[paymentv1.UpdateQrCodeRequest],
) (*connect.Response[paymentv1.UpdateQrCodeResponse], error) {
	user, ok := rpcinterceptor.UserFromContext(ctx)
	if !ok {
		return nil, paymentError()
	}

	if r.Msg.Channel == paymentv1.Channel_CHANNEL_UNSPECIFIED {
		return nil, invalidChannelError()
	}

	if r.Msg.Content == "" {
		return nil, paymentError()
	}

	qrCode, err := service.UpdateQrCode(ctx, user.UserID, r.Msg.Channel, r.Msg.Content)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to update QR code for userID: %d, channel: %v", user.UserID, r.Msg.Channel)
		return nil, mapServiceError(err)
	}

	return connect.NewResponse(&paymentv1.UpdateQrCodeResponse{
		QrCode: qrCode,
	}), nil
}

func InitQrCodeServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := paymentv1connect.NewQrCodeServiceHandler(&QrCodeServiceServer{}, opts...)
	log.Debug().Msgf("QrCodeService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
