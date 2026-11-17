package v1

import (
	"context"
	"crypto/subtle"
	"net/http"
	"os"

	ac "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/admin/v1/adminv1connect"
	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/adminflow"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/service"
	"github.com/labstack/echo/v5"
)

type PaymentCorrectionInternalServer struct{}

func internalAdminContext(ctx context.Context, headers http.Header, actorID string) (context.Context, error) {
	expected := os.Getenv("ADMIN_INTERNAL_TOKEN")
	if len(expected) < 32 ||
		subtle.ConstantTimeCompare([]byte(expected), []byte(headers.Get(adminflow.InternalHeader))) != 1 {
		return nil, adminflow.Failure(connect.CodePermissionDenied, "内部服务认证失败")
	}
	return service.SourceAdminContext(ctx, actorID)
}

func (s *PaymentCorrectionInternalServer) GetSourceBills(
	ctx context.Context,
	r *connect.Request[av.GetSourceBillsRequest],
) (*connect.Response[av.GetSourceBillsResponse], error) {
	ctx, err := internalAdminContext(ctx, r.Header(), r.Msg.ActorId)
	if err != nil {
		return nil, err
	}
	result, err := service.AdminSourceBills(ctx, r.Msg)
	if err != nil {
		return nil, adminPaymentError(err)
	}
	return connect.NewResponse(result), nil
}

func (s *PaymentCorrectionInternalServer) CorrectSourceBills(
	ctx context.Context,
	r *connect.Request[av.CorrectSourceBillsRequest],
) (*connect.Response[av.CorrectSourceBillsResponse], error) {
	ctx, err := internalAdminContext(ctx, r.Header(), r.Msg.ActorId)
	if err != nil {
		return nil, err
	}
	result, err := service.CorrectSourceBills(ctx, r.Msg)
	if err != nil {
		return nil, adminPaymentError(err)
	}
	return connect.NewResponse(result), nil
}

func InitPaymentCorrectionInternalHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	path, handler := ac.NewPaymentCorrectionInternalServiceHandler(&PaymentCorrectionInternalServer{}, opts...)
	e.Any(path+"*", echo.WrapHandler(handler))
}
