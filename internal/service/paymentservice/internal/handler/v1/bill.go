package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/payment/v1/paymentv1connect"
	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/service"
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
	bill, err := service.CreateBill(
		ctx,
		r.Msg.PayerId,
		r.Msg.PayeeId,
		r.Msg.AmountCents,
		r.Msg.SourceType,
		r.Msg.SourceId,
	)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&paymentv1.CreateBillResponse{
		Bill: bill,
	}), nil
}

func (s *BillServiceServer) PayBill(
	ctx context.Context,
	r *connect.Request[paymentv1.PayBillRequest],
) (*connect.Response[paymentv1.PayBillResponse], error) {
	bill, err := service.PayBill(ctx, r.Msg.BillId, r.Msg.Channel, r.Msg.UpdatedAt.AsTime())
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&paymentv1.PayBillResponse{
		Bill: bill,
	}), nil
}

func (s *BillServiceServer) ConfirmBill(
	ctx context.Context,
	r *connect.Request[paymentv1.ConfirmBillRequest],
) (*connect.Response[paymentv1.ConfirmBillResponse], error) {
	bill, err := service.ConfirmBill(ctx, r.Msg.BillId, r.Msg.UpdatedAt.AsTime())
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&paymentv1.ConfirmBillResponse{
		Bill: bill,
	}), nil
}

func (s *BillServiceServer) GetBill(
	ctx context.Context,
	r *connect.Request[paymentv1.GetBillRequest],
) (*connect.Response[paymentv1.GetBillResponse], error) {
	bill, err := service.GetBill(ctx, r.Msg.BillId)
	if err != nil {
		return nil, mapServiceError(err)
	}

	return connect.NewResponse(&paymentv1.GetBillResponse{
		Bill: bill,
	}), nil
}

func (s *BillServiceServer) TransitionBill(
	ctx context.Context,
	r *connect.Request[paymentv1.TransitionBillRequest],
) (*connect.Response[paymentv1.TransitionBillResponse], error) {
	authUser, ok := interceptor.UserFromContext(ctx)
	if !ok {
		return nil, paymentError()
	}

	bill, err := service.TransitionBill(
		ctx,
		r.Msg.BillId,
		r.Msg.TargetStatus,
		r.Msg.UpdatedAt.AsTime(),
		authUser.UserID,
	)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&paymentv1.TransitionBillResponse{
		Bill: bill,
	}), nil
}

func (s *BillServiceServer) SupplementSerialNumber(
	ctx context.Context,
	r *connect.Request[paymentv1.SupplementSerialNumberRequest],
) (*connect.Response[paymentv1.SupplementSerialNumberResponse], error) {
	bill, err := service.SupplementSerialNumber(ctx, r.Msg.BillId, r.Msg.SerialNumber, r.Msg.UpdatedAt.AsTime())
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&paymentv1.SupplementSerialNumberResponse{
		Bill: bill,
	}), nil
}

func InitBillServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := paymentv1connect.NewBillServiceHandler(&BillServiceServer{}, opts...)
	log.Debug().Msgf("BillService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
