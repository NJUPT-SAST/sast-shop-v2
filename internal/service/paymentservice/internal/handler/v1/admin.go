package v1

import (
	"context"

	ac "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/admin/v1/adminv1connect"
	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/adminflow"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/service"
	"github.com/labstack/echo/v5"
)

type PaymentAdminServer struct{ flow *adminflow.Engine }

func adminPaymentError(err error) error { return service.AdminPaymentError(err) }

func (s *PaymentAdminServer) ListRecords(
	ctx context.Context,
	r *connect.Request[av.ListRecordsRequest],
) (*connect.Response[av.ListRecordsResponse], error) {
	result, err := service.AdminListBills(ctx, r.Msg)
	if err != nil {
		return nil, adminPaymentError(err)
	}
	return connect.NewResponse(result), nil
}

func (s *PaymentAdminServer) GetRecord(
	ctx context.Context,
	r *connect.Request[av.GetRecordRequest],
) (*connect.Response[av.GetRecordResponse], error) {
	result, err := service.AdminGetBill(ctx, r.Msg)
	if err != nil {
		return nil, adminPaymentError(err)
	}
	return connect.NewResponse(result), nil
}

func (s *PaymentAdminServer) PreviewCorrection(
	ctx context.Context,
	r *connect.Request[av.PreviewCorrectionRequest],
) (*connect.Response[av.PreviewCorrectionResponse], error) {
	result, err := s.flow.Preview(ctx, r.Msg)
	if err != nil {
		return nil, adminPaymentError(err)
	}
	return connect.NewResponse(result), nil
}

func (s *PaymentAdminServer) ExecuteCorrection(
	ctx context.Context,
	r *connect.Request[av.ExecuteCorrectionRequest],
) (*connect.Response[av.ExecuteCorrectionResponse], error) {
	result, err := s.flow.Execute(ctx, r.Msg)
	if err != nil {
		return nil, adminPaymentError(err)
	}
	return connect.NewResponse(result), nil
}

func (s *PaymentAdminServer) GetCorrection(
	ctx context.Context,
	r *connect.Request[av.GetCorrectionRequest],
) (*connect.Response[av.GetCorrectionResponse], error) {
	result, err := s.flow.Get(ctx, r.Msg)
	if err != nil {
		return nil, adminPaymentError(err)
	}
	return connect.NewResponse(result), nil
}

func (s *PaymentAdminServer) ListCorrections(
	ctx context.Context,
	r *connect.Request[av.ListCorrectionsRequest],
) (*connect.Response[av.ListCorrectionsResponse], error) {
	result, err := s.flow.List(ctx, r.Msg)
	if err != nil {
		return nil, adminPaymentError(err)
	}
	return connect.NewResponse(result), nil
}

func (s *PaymentAdminServer) ListAudit(
	ctx context.Context,
	r *connect.Request[av.ListAuditRequest],
) (*connect.Response[av.ListAuditResponse], error) {
	result, err := admin.ListAudit(ctx, postgres.DB, "payment", r.Msg)
	if err != nil {
		return nil, adminPaymentError(err)
	}
	return connect.NewResponse(result), nil
}

func (s *PaymentAdminServer) GetStats(
	ctx context.Context,
	r *connect.Request[av.GetStatsRequest],
) (*connect.Response[av.GetStatsResponse], error) {
	result, err := service.AdminPaymentStats(ctx)
	if err != nil {
		return nil, adminPaymentError(err)
	}
	return connect.NewResponse(result), nil
}

func (s *PaymentAdminServer) MutateRecord(
	ctx context.Context,
	r *connect.Request[av.MutateRecordRequest],
) (*connect.Response[av.MutateRecordResponse], error) {
	if _, err := admin.Require(ctx); err != nil {
		return nil, err
	}
	return nil, adminflow.Failure(connect.CodeInvalidArgument, "账单操作需要先核查纠错预览")
}

func InitPaymentAdminHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	flow := service.AdminPaymentEngine()
	path, handler := ac.NewPaymentAdminServiceHandler(&PaymentAdminServer{flow: flow}, opts...)
	e.Any(path+"*", echo.WrapHandler(handler))
	go flow.Run(context.Background())
	go service.RunPaymentOutbox(context.Background())
}
