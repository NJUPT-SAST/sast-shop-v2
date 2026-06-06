package v1

import (
	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
)

func userError() *connect.Error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_UserError{
		UserError: &userv1.UserError{
			Code: userv1.UserErrorCode_USER_ERROR_CODE_INTERNAL_ERROR,
		},
	}, "")
}
