package service

import (
	"context"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/repository"
	"github.com/rs/zerolog/log"
)

func GetProductTemplate(ctx context.Context, id int64) (*catalogv1.ProductTemplate, error) {
	pt, err := repository.GetProductTemplateByID(ctx, id)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get product template for id: %d", id)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	return &catalogv1.ProductTemplate{
		Id:          pt.ID,
		Title:       pt.Title,
		Description: pt.Description,
		PriceCents:  pt.PriceCents,
		StoreId:     pt.StoreID,
	}, nil
}

func GetStore(ctx context.Context, id int64) (*catalogv1.Store, error) {
	store, err := repository.GetStoreByID(ctx, id)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get store for id: %d", id)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	return &catalogv1.Store{
		Id:         store.ID,
		Name:       store.Name,
		Address:    store.Address,
		LogoUrl:    store.LogoURL,
		ThemeColor: store.ThemeColor,
	}, nil
}
