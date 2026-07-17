package service

import (
	"context"
	"database/sql"
	"errors"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/repository"
	"github.com/rs/zerolog/log"
)

// 哨兵错误
var (
	ErrStoreNotFound = errors.New("store not found")
)

// storeToProto 将 DB model 转为 proto Store。
func storeToProto(s *model.CatalogStore) *catalogv1.Store {
	return &catalogv1.Store{
		Id:         s.ID,
		Name:       s.Name,
		Address:    s.Address,
		LogoUrl:    s.LogoURL,
		ThemeColor: s.ThemeColor,
	}
}

// GetProductTemplate 按 ID 获取商品模板。
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

// GetStore 按 ID 获取店铺。
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
	return storeToProto(store), nil
}

// ————— 店铺 CRUD —————

// GetStoreList 查询所有店铺。
func GetStoreList(ctx context.Context) ([]*catalogv1.Store, error) {
	stores, err := repository.ListStores(ctx)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list stores")
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	result := make([]*catalogv1.Store, 0, len(stores))
	for _, s := range stores {
		result = append(result, storeToProto(s))
	}
	return result, nil
}

// CreateStore 创建店铺，createdByUserID 来自当前登录用户。
func CreateStore(
	ctx context.Context,
	name, address, logoURL, themeColor string,
	createdByUserID int64,
) (*catalogv1.Store, error) {
	store := &model.CatalogStore{
		Name:            name,
		Address:         address,
		LogoURL:         logoURL,
		ThemeColor:      themeColor,
		Status:          model.CatalogStatusActive,
		CreatedByUserID: createdByUserID,
	}
	if err := repository.CreateStore(ctx, store); err != nil {
		log.Error().Err(err).Msg("Failed to create store")
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	return storeToProto(store), nil
}

// UpdateStore 部分更新店铺，updateMask 指定要更新的字段。
func UpdateStore(
	ctx context.Context,
	store *catalogv1.Store,
	updateMask []string,
) (*catalogv1.Store, error) {
	existing, err := repository.GetStoreByID(ctx, store.Id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrStoreNotFound
		}
		log.Error().Err(err).Msgf("Failed to get store for update: %d", store.Id)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	updates := buildStoreUpdates(store, updateMask)
	if len(updates) == 0 {
		return storeToProto(existing), nil
	}

	if err := repository.UpdateStore(ctx, store.Id, updates); err != nil {
		log.Error().Err(err).Msgf("Failed to update store: %d", store.Id)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	applyStoreUpdates(existing, updates)
	return storeToProto(existing), nil
}

// ————— FieldMask 映射 —————

func buildStoreUpdates(store *catalogv1.Store, maskPaths []string) map[string]any {
	updates := make(map[string]any)
	for _, path := range maskPaths {
		switch path {
		case "name":
			updates["name"] = store.Name
		case "address":
			updates["address"] = store.Address
		case "logo_url":
			updates["logo_url"] = store.LogoUrl
		case "theme_color":
			updates["theme_color"] = store.ThemeColor
		}
	}
	return updates
}

func applyStoreUpdates(store *model.CatalogStore, updates map[string]any) {
	if v, ok := updates["name"]; ok {
		if s, ok2 := v.(string); ok2 {
			store.Name = s
		}
	}
	if v, ok := updates["address"]; ok {
		if s, ok2 := v.(string); ok2 {
			store.Address = s
		}
	}
	if v, ok := updates["logo_url"]; ok {
		if s, ok2 := v.(string); ok2 {
			store.LogoURL = s
		}
	}
	if v, ok := updates["theme_color"]; ok {
		if s, ok2 := v.(string); ok2 {
			store.ThemeColor = s
		}
	}
}
