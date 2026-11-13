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

type PaymentInternalServer struct {
	paymentv1connect.PaymentInternalServiceHandler
}

func (s *PaymentInternalServer) GetPayeeQrCode(
	ctx context.Context,
	r *connect.Request[paymentv1.GetPayeeQrCodeRequest],
) (*connect.Response[paymentv1.GetPayeeQrCodeResponse], error) {
	if err := interceptor.RequireWestPocketService(r.Header()); err != nil {
		return nil, err
	}
	result, err := service.GetPayeeQrCode(ctx, r.Msg.OwnerId)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(result), nil
}

func (s *PaymentInternalServer) CancelWestPocketBillsIfUnpaid(
	ctx context.Context,
	r *connect.Request[paymentv1.CancelWestPocketBillsIfUnpaidRequest],
) (*connect.Response[paymentv1.CancelWestPocketBillsIfUnpaidResponse], error) {
	if err := interceptor.RequireWestPocketService(r.Header()); err != nil {
		return nil, err
	}
	if err := service.CancelWestPocketBillsIfUnpaid(ctx, r.Msg.PocketId); err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&paymentv1.CancelWestPocketBillsIfUnpaidResponse{}), nil
}

func (s *PaymentInternalServer) CreateBillForOrder(
	ctx context.Context,
	r *connect.Request[paymentv1.CreateBillForOrderRequest],
) (*connect.Response[paymentv1.CreateBillForOrderResponse], error) {
	if r.Msg.SourceType == "west_pocket" {
		if err := interceptor.RequireWestPocketService(r.Header()); err != nil {
			return nil, err
		}
	}
	bill, err := service.CreateBillForOrder(
		ctx,
		r.Msg.GetSourceType(),
		r.Msg.GetSourceId(),
		r.Msg.GetPayerId(),
		r.Msg.GetPayeeId(),
		r.Msg.GetAmountCents(),
	)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&paymentv1.CreateBillForOrderResponse{
		Bill: bill,
	}), nil
}

func (s *PaymentInternalServer) CancelBillBySource(
	ctx context.Context,
	r *connect.Request[paymentv1.CancelBillBySourceRequest],
) (*connect.Response[paymentv1.CancelBillBySourceResponse], error) {
	// West Pocket requires an atomic all-member cancellation; the generic
	// method accepts payer subsets and must never bypass that policy.
	if r.Msg.SourceType == "west_pocket" {
		return nil, invalidBillStatusError()
	}
	err := service.CancelBillBySource(
		ctx,
		r.Msg.GetSourceType(),
		r.Msg.GetSourceId(),
		r.Msg.PayerId,
	)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&paymentv1.CancelBillBySourceResponse{}), nil
}

func (s *PaymentInternalServer) BatchGetBills(
	ctx context.Context,
	r *connect.Request[paymentv1.BatchGetBillsRequest],
) (*connect.Response[paymentv1.BatchGetBillsResponse], error) {
	if r.Header().Get(interceptor.WestPocketServiceHeader) != "" {
		if err := interceptor.RequireWestPocketService(r.Header()); err != nil {
			return nil, err
		}
	}
	bills, err := service.BatchGetBills(ctx, r.Msg.GetBillIds())
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&paymentv1.BatchGetBillsResponse{
		Bills: bills,
	}), nil
}

func InitPaymentInternalServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := paymentv1connect.NewPaymentInternalServiceHandler(&PaymentInternalServer{}, opts...)
	log.Debug().Msgf("PaymentInternalService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
