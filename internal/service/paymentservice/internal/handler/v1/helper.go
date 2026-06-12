//nolint:unused // Functions will be used in Phase 2 (BillService/PaymentInternalService handlers)
package v1

import (
	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
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
