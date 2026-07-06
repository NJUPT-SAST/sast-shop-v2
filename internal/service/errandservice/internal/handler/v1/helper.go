package v1

import (
	"context"
	"errors"

	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	rpcinterceptor "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/service"
)

func errandError() *connect.Error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_ErrandError{
		ErrandError: &errandv1.ErrandError{
			Code: errandv1.ErrandErrorCode_ERRAND_ERROR_CODE_INTERNAL_ERROR,
		},
	}, "")
}

func captainIDFromContext(ctx context.Context) (int64, error) {
	user, ok := rpcinterceptor.UserFromContext(ctx)
	if !ok || user == nil || user.UserID <= 0 {
		return 0, connect.NewError(connect.CodeUnauthenticated, errors.New("missing authenticated user"))
	}
	return user.UserID, nil
}

func mapServiceError(err error) error {
	if err == nil {
		return nil
	}

	var connErr *connect.Error
	if errors.As(err, &connErr) {
		return connErr
	}

	switch {
	case errors.Is(err, service.ErrInvalidDemandItem),
		errors.Is(err, service.ErrStoreMismatch):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, service.ErrDemandItemNotOpen):
		return connect.NewError(connect.CodeFailedPrecondition, err)

	case errors.Is(err, service.ErrConcurrencyConflict):
		return connect.NewError(connect.CodeAborted, err)

	default:
		return errandError()
	}
}
