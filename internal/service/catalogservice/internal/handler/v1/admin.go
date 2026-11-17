package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/admin/v1/adminv1connect"
	adminv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/service"
	"github.com/labstack/echo/v5"
)

type CatalogAdminServer struct {
	adminv1connect.UnimplementedCatalogAdminServiceHandler
}

func (s *CatalogAdminServer) ListRecords(
	ctx context.Context,
	req *connect.Request[adminv1.ListRecordsRequest],
) (*connect.Response[adminv1.ListRecordsResponse], error) {
	result, err := service.ListAdminRecords(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(result), nil
}

func (s *CatalogAdminServer) GetRecord(
	ctx context.Context,
	req *connect.Request[adminv1.GetRecordRequest],
) (*connect.Response[adminv1.GetRecordResponse], error) {
	result, err := service.GetAdminRecord(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(result), nil
}

func (s *CatalogAdminServer) MutateRecord(
	ctx context.Context,
	req *connect.Request[adminv1.MutateRecordRequest],
) (*connect.Response[adminv1.MutateRecordResponse], error) {
	result, err := service.MutateAdminRecord(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(result), nil
}

func (s *CatalogAdminServer) ListAudit(
	ctx context.Context,
	req *connect.Request[adminv1.ListAuditRequest],
) (*connect.Response[adminv1.ListAuditResponse], error) {
	result, err := admin.ListAudit(ctx, postgres.DB, "catalog", req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(result), nil
}

func (s *CatalogAdminServer) GetStats(
	ctx context.Context,
	req *connect.Request[adminv1.GetStatsRequest],
) (*connect.Response[adminv1.GetStatsResponse], error) {
	result, err := service.GetAdminStats(ctx)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(result), nil
}

func InitCatalogAdminHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	path, handler := adminv1connect.NewCatalogAdminServiceHandler(&CatalogAdminServer{}, opts...)
	e.Any(path+"*", echo.WrapHandler(handler))
}
