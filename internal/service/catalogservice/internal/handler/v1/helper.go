package v1

import (
	"errors"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/service"
)

func catalogError() *connect.Error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
		CatalogError: &catalogv1.CatalogError{
			Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
		},
	}, "")
}

func storeNotFoundError() *connect.Error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
		CatalogError: &catalogv1.CatalogError{
			Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
		},
	}, "")
}

func productNotFoundError() *connect.Error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
		CatalogError: &catalogv1.CatalogError{
			Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
		},
	}, "")
}

func barcodeNotFoundError() *connect.Error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
		CatalogError: &catalogv1.CatalogError{
			Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
		},
	}, "")
}

// mapServiceError 将 service 层哨兵错误映射为 Connect 错误。
func mapServiceError(err error) *connect.Error {
	switch {
	case errors.Is(err, service.ErrStoreNotFound):
		return storeNotFoundError()
	case errors.Is(err, service.ErrProductNotFound):
		return productNotFoundError()
	case errors.Is(err, service.ErrBarcodeNotFound):
		return barcodeNotFoundError()
	default:
		return catalogError()
	}
}
