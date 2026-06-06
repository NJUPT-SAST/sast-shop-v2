package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/payment/v1/paymentv1connect"
	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	"connectrpc.com/connect"
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
	return nil, paymentError()
}

func (s *QrCodeServiceServer) UpdateQrCode(
	ctx context.Context,
	r *connect.Request[paymentv1.UpdateQrCodeRequest],
) (*connect.Response[paymentv1.UpdateQrCodeResponse], error) {
	return nil, paymentError()
}

func InitQrCodeServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := paymentv1connect.NewQrCodeServiceHandler(&QrCodeServiceServer{}, opts...)
	log.Debug().Msgf("QrCodeService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
