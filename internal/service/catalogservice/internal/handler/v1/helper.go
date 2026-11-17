package v1

import (
	"context"
	"errors"
	"strings"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/errmsg"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/service"
)

// Catalog maintenance is shared by all active member roles, including administrators.
func requireCatalogMember(ctx context.Context) (*interceptor.AuthUser, error) {
	user, ok := interceptor.UserFromContext(ctx)
	if !ok || user.UserID <= 0 || !strings.EqualFold(strings.TrimSpace(user.Status), "active") {
		return nil, connect.NewError(errmsg.Unauthenticated.Code, errmsg.Unauthenticated)
	}
	if !catalogRoleAllowed(user.Role) {
		return nil, connect.NewError(errmsg.PermissionDenied.Code, errmsg.PermissionDenied)
	}
	return user, nil
}

func catalogRoleAllowed(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user", "admin", "superadmin":
		return true
	default:
		return false
	}
}

func catalogError() *connect.Error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
		CatalogError: &catalogv1.CatalogError{
			Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
		},
	}, errmsg.Internal.Msg)
}

func storeNotFoundError() *connect.Error {
	return connect.NewError(errmsg.StoreNotFound.Code, errmsg.StoreNotFound)
}

func productNotFoundError() *connect.Error {
	return connect.NewError(errmsg.ProductTemplateNotFound.Code, errmsg.ProductTemplateNotFound)
}

func barcodeNotFoundError() *connect.Error {
	return connect.NewError(errmsg.BarcodeNotFound.Code, errmsg.BarcodeNotFound)
}

func productTemplateForbiddenError() *connect.Error {
	return connect.NewError(errmsg.ProductTemplateForbidden.Code, errmsg.ProductTemplateForbidden)
}

// mapServiceError 将 service 层哨兵错误映射为 Connect 错误。
func mapServiceError(err error) *connect.Error {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connectErr
	}
	switch {
	case errors.Is(err, service.ErrStoreNotFound):
		return storeNotFoundError()
	case errors.Is(err, service.ErrProductNotFound):
		return productNotFoundError()
	case errors.Is(err, service.ErrBarcodeNotFound):
		return barcodeNotFoundError()
	case errors.Is(err, service.ErrProductTemplateForbidden):
		return productTemplateForbiddenError()
	default:
		return catalogError()
	}
}
