package rpcerror

import (
	"errors"

	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
)

// NewError creates a connect error with a BusinessError detail.
// The detail must be a protobuf oneof wrapper like &commonv1.BusinessError_UserError{...}.
func NewError(code connect.Code, detail any, errorMessage string) *connect.Error {
	if errorMessage == "" {
		errorMessage = constant.UnknownErrorMessage
	}
	connErr := connect.NewError(code, errors.New(errorMessage))
	bizErr := &commonv1.BusinessError{
		ErrorMessage: errorMessage,
	}
	switch d := detail.(type) {
	case *commonv1.BusinessError_UserError:
		bizErr.Detail = d
	case *commonv1.BusinessError_CatalogError:
		bizErr.Detail = d
	case *commonv1.BusinessError_PaymentError:
		bizErr.Detail = d
	case *commonv1.BusinessError_SpotError:
		bizErr.Detail = d
	case *commonv1.BusinessError_ErrandError:
		bizErr.Detail = d
	}
	if detail, e := connect.NewErrorDetail(bizErr); e == nil {
		connErr.AddDetail(detail)
	}
	return connErr
}

// NewInternalError creates a connect error with CodeInternal and a BusinessError detail.
func NewInternalError(detail any, errorMessage string) *connect.Error {
	return NewError(connect.CodeInternal, detail, errorMessage)
}
