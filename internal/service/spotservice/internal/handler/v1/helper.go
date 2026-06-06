package v1

import (
	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
)

func spotError() *connect.Error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
		SpotError: &spotv1.SpotError{
			Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
		},
	}, "")
}
