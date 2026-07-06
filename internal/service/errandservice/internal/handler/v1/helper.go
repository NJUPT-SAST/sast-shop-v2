package v1

import (
	"errors"

	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
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

func mapServiceError(err error) error{
	switch{
	case errors.Is(err,service.ErrInvalidDemandItem):
		return connect.NewError(connect.CodeInvalidArgument,err)
	case errors.Is(err,service.ErrStoreMismatch):
		return connect.NewError(connect.CodeInvalidArgument,err)
	case errors.Is(err,service.ErrConcurrencyConflict):
		return connect.NewError(connect.CodeAborted,err)
	case errors.Is(err,service.ErrDemandItemNotOpen):
		return connect.NewError(connect.CodeFailedPrecondition,err)
	}
}