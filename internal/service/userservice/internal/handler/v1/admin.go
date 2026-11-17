package v1

import (
	"context"
	"errors"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/admin/v1/adminv1connect"
	adminv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/service"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type UserAdminServer struct{}

func InitUserAdminHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	path, handler := adminv1connect.NewUserAdminServiceHandler(&UserAdminServer{}, opts...)
	e.Any(path+"*", echo.WrapHandler(handler))
}

func userAdminError(err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *connect.Error
	if errors.As(err, &rpcErr) {
		return err
	}
	log.Error().Err(err).Msg("user administration request failed")
	return connect.NewError(connect.CodeInternal, errors.New("管理请求失败，请稍后重试"))
}

func (*UserAdminServer) GetContext(
	ctx context.Context,
	_ *connect.Request[adminv1.GetContextRequest],
) (*connect.Response[adminv1.GetContextResponse], error) {
	out, err := service.AdminContext(ctx)
	if err != nil {
		return nil, userAdminError(err)
	}
	return connect.NewResponse(out), nil
}

func (*UserAdminServer) ListRecords(
	ctx context.Context,
	r *connect.Request[adminv1.ListRecordsRequest],
) (*connect.Response[adminv1.ListRecordsResponse], error) {
	out, err := service.AdminListUsers(ctx, r.Msg)
	if err != nil {
		return nil, userAdminError(err)
	}
	return connect.NewResponse(out), nil
}

func (*UserAdminServer) GetRecord(
	ctx context.Context,
	r *connect.Request[adminv1.GetRecordRequest],
) (*connect.Response[adminv1.GetRecordResponse], error) {
	out, err := service.AdminGetUser(ctx, r.Msg)
	if err != nil {
		return nil, userAdminError(err)
	}
	return connect.NewResponse(out), nil
}

func (*UserAdminServer) MutateRecord(
	ctx context.Context,
	r *connect.Request[adminv1.MutateRecordRequest],
) (*connect.Response[adminv1.MutateRecordResponse], error) {
	out, err := service.AdminMutateUser(ctx, r.Msg)
	if err != nil {
		return nil, userAdminError(err)
	}
	return connect.NewResponse(out), nil
}

func (*UserAdminServer) ListAudit(
	ctx context.Context,
	r *connect.Request[adminv1.ListAuditRequest],
) (*connect.Response[adminv1.ListAuditResponse], error) {
	if r.Msg.Resource != adminv1.ResourceKind_RESOURCE_KIND_UNSPECIFIED &&
		r.Msg.Resource != adminv1.ResourceKind_RESOURCE_KIND_USER {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("仅支持用户资源"))
	}
	out, err := admin.ListAudit(ctx, postgres.DB, "user", r.Msg)
	if err != nil {
		return nil, userAdminError(err)
	}
	return connect.NewResponse(out), nil
}

func (*UserAdminServer) GetStats(
	ctx context.Context,
	_ *connect.Request[adminv1.GetStatsRequest],
) (*connect.Response[adminv1.GetStatsResponse], error) {
	out, err := service.AdminUserStats(ctx)
	if err != nil {
		return nil, userAdminError(err)
	}
	return connect.NewResponse(out), nil
}

func unsupportedUserCorrection(ctx context.Context) error {
	if _, err := admin.Require(ctx); err != nil {
		return err
	}
	return connect.NewError(connect.CodeInvalidArgument, errors.New("用户账号不支持交易纠错，请使用账号管理操作"))
}

func (*UserAdminServer) PreviewCorrection(
	ctx context.Context,
	_ *connect.Request[adminv1.PreviewCorrectionRequest],
) (*connect.Response[adminv1.PreviewCorrectionResponse], error) {
	return nil, unsupportedUserCorrection(ctx)
}

func (*UserAdminServer) ExecuteCorrection(
	ctx context.Context,
	_ *connect.Request[adminv1.ExecuteCorrectionRequest],
) (*connect.Response[adminv1.ExecuteCorrectionResponse], error) {
	return nil, unsupportedUserCorrection(ctx)
}

func (*UserAdminServer) GetCorrection(
	ctx context.Context,
	_ *connect.Request[adminv1.GetCorrectionRequest],
) (*connect.Response[adminv1.GetCorrectionResponse], error) {
	return nil, unsupportedUserCorrection(ctx)
}

func (*UserAdminServer) ListCorrections(
	ctx context.Context,
	_ *connect.Request[adminv1.ListCorrectionsRequest],
) (*connect.Response[adminv1.ListCorrectionsResponse], error) {
	return nil, unsupportedUserCorrection(ctx)
}
