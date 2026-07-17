package v1

import (
	"errors"
	"time"

	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func paymentError() *connect.Error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_PaymentError{
		PaymentError: &paymentv1.PaymentError{
			Code: paymentv1.PaymentErrorCode_PAYMENT_ERROR_CODE_UNSPECIFIED,
		},
	}, "")
}

func billNotFoundError() *connect.Error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_PaymentError{
		PaymentError: &paymentv1.PaymentError{
			Code: paymentv1.PaymentErrorCode_PAYMENT_ERROR_CODE_BILL_NOT_FOUND,
		},
	}, "")
}

func invalidBillStatusError() *connect.Error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_PaymentError{
		PaymentError: &paymentv1.PaymentError{
			Code: paymentv1.PaymentErrorCode_PAYMENT_ERROR_CODE_INVALID_BILL_STATUS,
		},
	}, "")
}

func invalidChannelError() *connect.Error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_PaymentError{
		PaymentError: &paymentv1.PaymentError{
			Code: paymentv1.PaymentErrorCode_PAYMENT_ERROR_CODE_INVALID_CHANNEL,
		},
	}, "")
}

func duplicateBillError() *connect.Error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_PaymentError{
		PaymentError: &paymentv1.PaymentError{
			Code: paymentv1.PaymentErrorCode_PAYMENT_ERROR_CODE_DUPLICATE_BILL,
		},
	}, "")
}

func mapServiceError(err error) *connect.Error {
	var connErr *connect.Error
	if errors.As(err, &connErr) {
		return connErr
	}

	switch {
	case errors.Is(err, service.ErrBillNotFound):
		return billNotFoundError()
	case errors.Is(err, service.ErrInvalidBillStatus):
		return invalidBillStatusError()
	case errors.Is(err, service.ErrInvalidChannel):
		return invalidChannelError()
	case errors.Is(err, service.ErrDuplicateBill):
		return duplicateBillError()
	case errors.Is(err, service.ErrConcurrencyConflict):
		return paymentError()
	default:
		return paymentError()
	}
}

// requireUpdatedAt 提取 UpdatedAt，为 nil 时返回错误避免 panic。
func requireUpdatedAt(ts *timestamppb.Timestamp) (time.Time, *connect.Error) {
	if ts == nil {
		return time.Time{}, paymentError()
	}
	return ts.AsTime(), nil
}
