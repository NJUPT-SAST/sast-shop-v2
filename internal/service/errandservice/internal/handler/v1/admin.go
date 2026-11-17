package v1

import (
	"context"
	"errors"

	ac "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/admin/v1/adminv1connect"
	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/service"
	"github.com/labstack/echo/v5"
)

type ErrandAdminServer struct{}

func InitErrandAdminHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	path, h := ac.NewErrandAdminServiceHandler(&ErrandAdminServer{}, opts...)
	e.Any(path+"*", echo.WrapHandler(h))
	go service.AdminErrandEngine().Run(context.Background())
}

func (h *ErrandAdminServer) ListRecords(
	ctx context.Context,
	r *connect.Request[av.ListRecordsRequest],
) (*connect.Response[av.ListRecordsResponse], error) {
	out, e := service.AdminErrandList(ctx, r.Msg)
	if e != nil {
		return nil, mapServiceError(e)
	}
	return connect.NewResponse(out), nil
}

func (h *ErrandAdminServer) GetRecord(
	ctx context.Context,
	r *connect.Request[av.GetRecordRequest],
) (*connect.Response[av.GetRecordResponse], error) {
	out, e := service.AdminErrandGet(ctx, r.Msg)
	if e != nil {
		return nil, mapServiceError(e)
	}
	return connect.NewResponse(out), nil
}

func (h *ErrandAdminServer) PreviewCorrection(
	ctx context.Context,
	r *connect.Request[av.PreviewCorrectionRequest],
) (*connect.Response[av.PreviewCorrectionResponse], error) {
	out, e := service.AdminErrandEngine().Preview(ctx, r.Msg)
	if e != nil {
		return nil, mapServiceError(e)
	}
	return connect.NewResponse(out), nil
}

func (h *ErrandAdminServer) ExecuteCorrection(
	ctx context.Context,
	r *connect.Request[av.ExecuteCorrectionRequest],
) (*connect.Response[av.ExecuteCorrectionResponse], error) {
	out, e := service.AdminErrandEngine().Execute(ctx, r.Msg)
	if e != nil {
		return nil, mapServiceError(e)
	}
	return connect.NewResponse(out), nil
}

func (h *ErrandAdminServer) GetCorrection(
	ctx context.Context,
	r *connect.Request[av.GetCorrectionRequest],
) (*connect.Response[av.GetCorrectionResponse], error) {
	out, e := service.AdminErrandEngine().Get(ctx, r.Msg)
	if e != nil {
		return nil, mapServiceError(e)
	}
	return connect.NewResponse(out), nil
}

func (h *ErrandAdminServer) ListCorrections(
	ctx context.Context,
	r *connect.Request[av.ListCorrectionsRequest],
) (*connect.Response[av.ListCorrectionsResponse], error) {
	out, e := service.AdminErrandEngine().List(ctx, r.Msg)
	if e != nil {
		return nil, mapServiceError(e)
	}
	return connect.NewResponse(out), nil
}

func (h *ErrandAdminServer) ListAudit(
	ctx context.Context,
	r *connect.Request[av.ListAuditRequest],
) (*connect.Response[av.ListAuditResponse], error) {
	out, e := admin.ListAudit(ctx, postgres.DB, "errand", r.Msg)
	if e != nil {
		return nil, mapServiceError(e)
	}
	return connect.NewResponse(out), nil
}

func (h *ErrandAdminServer) GetStats(
	ctx context.Context,
	r *connect.Request[av.GetStatsRequest],
) (*connect.Response[av.GetStatsResponse], error) {
	out, e := service.AdminErrandStats(ctx)
	if e != nil {
		return nil, mapServiceError(e)
	}
	return connect.NewResponse(out), nil
}

func (*ErrandAdminServer) MutateRecord(
	ctx context.Context,
	_ *connect.Request[av.MutateRecordRequest],
) (*connect.Response[av.MutateRecordResponse], error) {
	if _, e := admin.Require(ctx); e != nil {
		return nil, e
	}
	return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("跑腿记录变更须先生成整单纠错预览"))
}
