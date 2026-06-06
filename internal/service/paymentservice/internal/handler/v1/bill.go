package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/payment/v1/paymentv1connect"
	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	"connectrpc.com/connect"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type BillServiceServer struct {
	paymentv1connect.BillServiceHandler
}

func (s *BillServiceServer) CreateBill(
	ctx context.Context,
	r *connect.Request[paymentv1.CreateBillRequest],
) (*connect.Response[paymentv1.CreateBillResponse], error) {
	return nil, paymentError()
}

func (s *BillServiceServer) PayBill(
	ctx context.Context,
	r *connect.Request[paymentv1.PayBillRequest],
) (*connect.Response[paymentv1.PayBillResponse], error) {
	return nil, paymentError()
}

func (s *BillServiceServer) ConfirmBill(
	ctx context.Context,
	r *connect.Request[paymentv1.ConfirmBillRequest],
) (*connect.Response[paymentv1.ConfirmBillResponse], error) {
	return nil, paymentError()
}

func (s *BillServiceServer) GetBill(
	ctx context.Context,
	r *connect.Request[paymentv1.GetBillRequest],
) (*connect.Response[paymentv1.GetBillResponse], error) {
	return nil, paymentError()
}

func (s *BillServiceServer) TransitionBill(
	ctx context.Context,
	r *connect.Request[paymentv1.TransitionBillRequest],
) (*connect.Response[paymentv1.TransitionBillResponse], error) {
	return nil, paymentError()
}

func (s *BillServiceServer) SupplementSerialNumber(
	ctx context.Context,
	r *connect.Request[paymentv1.SupplementSerialNumberRequest],
) (*connect.Response[paymentv1.SupplementSerialNumberResponse], error) {
	return nil, paymentError()
}

func InitBillServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := paymentv1connect.NewBillServiceHandler(&BillServiceServer{}, opts...)
	log.Debug().Msgf("BillService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
