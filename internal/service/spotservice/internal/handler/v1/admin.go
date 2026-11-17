package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/admin/v1/adminv1connect"
	adminv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/service"
	"github.com/labstack/echo/v5"
)

type SpotAdminServer struct {
	adminv1connect.UnimplementedSpotAdminServiceHandler
}

func (s *SpotAdminServer) ListRecords(
	ctx context.Context,
	req *connect.Request[adminv1.ListRecordsRequest],
) (*connect.Response[adminv1.ListRecordsResponse], error) {
	result, err := service.ListAdminRecords(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(result), nil
}

func (s *SpotAdminServer) GetRecord(
	ctx context.Context,
	req *connect.Request[adminv1.GetRecordRequest],
) (*connect.Response[adminv1.GetRecordResponse], error) {
	result, err := service.GetAdminRecord(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(result), nil
}

func (s *SpotAdminServer) MutateRecord(
	ctx context.Context,
	req *connect.Request[adminv1.MutateRecordRequest],
) (*connect.Response[adminv1.MutateRecordResponse], error) {
	result, err := service.MutateAdminRecord(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(result), nil
}

func (s *SpotAdminServer) ListAudit(
	ctx context.Context,
	req *connect.Request[adminv1.ListAuditRequest],
) (*connect.Response[adminv1.ListAuditResponse], error) {
	result, err := admin.ListAudit(ctx, postgres.DB, "spot", req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(result), nil
}

func (s *SpotAdminServer) GetStats(
	ctx context.Context,
	req *connect.Request[adminv1.GetStatsRequest],
) (*connect.Response[adminv1.GetStatsResponse], error) {
	result, err := service.GetAdminStats(ctx)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(result), nil
}

func InitSpotAdminHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	path, handler := adminv1connect.NewSpotAdminServiceHandler(&SpotAdminServer{}, opts...)
	e.Any(path+"*", echo.WrapHandler(handler))
}

func (s *SpotAdminServer) PreviewCorrection(
	ctx context.Context,
	req *connect.Request[adminv1.PreviewCorrectionRequest],
) (*connect.Response[adminv1.PreviewCorrectionResponse], error) {
	result, err := service.SpotCorrectionEngine().Preview(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(result), nil
}

func (s *SpotAdminServer) ExecuteCorrection(
	ctx context.Context,
	req *connect.Request[adminv1.ExecuteCorrectionRequest],
) (*connect.Response[adminv1.ExecuteCorrectionResponse], error) {
	result, err := service.SpotCorrectionEngine().Execute(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(result), nil
}

func (s *SpotAdminServer) GetCorrection(
	ctx context.Context,
	req *connect.Request[adminv1.GetCorrectionRequest],
) (*connect.Response[adminv1.GetCorrectionResponse], error) {
	result, err := service.SpotCorrectionEngine().Get(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(result), nil
}

func (s *SpotAdminServer) ListCorrections(
	ctx context.Context,
	req *connect.Request[adminv1.ListCorrectionsRequest],
) (*connect.Response[adminv1.ListCorrectionsResponse], error) {
	result, err := service.SpotCorrectionEngine().List(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(result), nil
}
