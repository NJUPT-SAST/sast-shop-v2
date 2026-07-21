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
	"google.golang.org/protobuf/types/known/timestamppb"
)

// 哨兵错误
var (
	ErrStoreNotFound   = errors.New("store not found")
	ErrProductNotFound = errors.New("product template not found")
	ErrBarcodeNotFound = errors.New("barcode not found")
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

// productTemplateToProto 将 DB model 转为 proto ProductTemplate。
func productTemplateToProto(
	pt *model.CatalogProductTemplate, barcode string, imageURL string,
) *catalogv1.ProductTemplate {
	return &catalogv1.ProductTemplate{
		Id:           pt.ID,
		Title:        pt.Title,
		Description:  pt.Description,
		PriceCents:   pt.PriceCents,
		StoreId:      pt.StoreID,
		MainImageUrl: imageURL,
		Barcode:      barcode,
		UpdatedAt:    timestamppb.New(pt.UpdatedAt),
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
	barcode, imageURL := fillBarcodeAndImage(ctx, pt.ID)
	return productTemplateToProto(pt, barcode, imageURL), nil
}

// GetProductTemplates 按 ID 批量获取商品模板。
func GetProductTemplates(ctx context.Context, ids []int64) ([]*catalogv1.ProductTemplate, error) {
	pts, err := repository.ListProductTemplatesByIDs(ctx, ids)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get product templates for ids: %v", ids)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	result := make([]*catalogv1.ProductTemplate, 0, len(pts))
	for _, pt := range pts {
		barcode, imageURL := fillBarcodeAndImage(ctx, pt.ID)
		result = append(result, productTemplateToProto(pt, barcode, imageURL))
	}
	return result, nil
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

// ————— 商品模板 CRUD —————

// GetProductTemplateList 分页查询指定店铺的商品模板。
func GetProductTemplateList(
	ctx context.Context,
	storeID int64,
	page, pageSize int32,
) ([]*catalogv1.ProductTemplate, int32, error) {
	total, err := repository.CountProductTemplates(ctx, storeID)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to count product templates for store: %d", storeID)
		return nil, 0, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	offset := int((page - 1) * pageSize)
	pts, err := repository.ListProductTemplates(ctx, storeID, offset, int(pageSize))
	if err != nil {
		log.Error().Err(err).Msgf("Failed to list product templates for store: %d", storeID)
		return nil, 0, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	result := make([]*catalogv1.ProductTemplate, 0, len(pts))
	for _, pt := range pts {
		barcode, imageURL := fillBarcodeAndImage(ctx, pt.ID)
		result = append(result, productTemplateToProto(pt, barcode, imageURL))
	}
	//nolint:gosec // total is a count, safe to cast
	return result, int32(total), nil
}

// CreateProductTemplate 创建商品模板（含条码和图片）。
func CreateProductTemplate(
	ctx context.Context,
	storeID int64,
	title, description string,
	priceCents int32,
	mainImageURL, barcode string,
	createdByUserID int64,
) (*catalogv1.ProductTemplate, error) {
	_, err := repository.GetStoreByID(ctx, storeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrStoreNotFound
		}
		log.Error().Err(err).Msgf("Failed to verify store exists: %d", storeID)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	pt := &model.CatalogProductTemplate{
		Title:           title,
		Description:     description,
		PriceCents:      priceCents,
		StoreID:         storeID,
		Status:          model.CatalogStatusActive,
		CreatedByUserID: createdByUserID,
	}
	if err := repository.CreateProductTemplate(ctx, pt); err != nil {
		log.Error().Err(err).Msg("Failed to create product template")
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	if barcode != "" {
		b := &model.CatalogProductBarcode{
			ProductTemplateID: pt.ID,
			Barcode:           barcode,
		}
		if err := repository.CreateBarcode(ctx, b); err != nil {
			log.Error().Err(err).Msg("Failed to create barcode")
		}
	}

	if mainImageURL != "" {
		img := &model.CatalogProductImage{
			ProductTemplateID: pt.ID,
			ImageURL:          mainImageURL,
			SortOrder:         0,
		}
		if err := repository.CreateImage(ctx, img); err != nil {
			log.Error().Err(err).Msg("Failed to create image")
		}
	}

	return productTemplateToProto(pt, barcode, mainImageURL), nil
}

// UpdateProductTemplate 部分更新商品模板。
func UpdateProductTemplate(
	ctx context.Context,
	pt *catalogv1.ProductTemplate,
	updateMask []string,
) (*catalogv1.ProductTemplate, error) {
	existing, err := repository.GetProductTemplateByID(ctx, pt.Id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrProductNotFound
		}
		log.Error().Err(err).Msgf("Failed to get product template for update: %d", pt.Id)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	updates := buildProductTemplateUpdates(pt, updateMask)
	if len(updates) == 0 {
		barcode, imageURL := fillBarcodeAndImage(ctx, pt.Id)
		return productTemplateToProto(existing, barcode, imageURL), nil
	}

	if err := repository.UpdateProductTemplate(ctx, pt.Id, updates); err != nil {
		log.Error().Err(err).Msgf("Failed to update product template: %d", pt.Id)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	applyProductTemplateUpdates(existing, updates)
	barcode, imageURL := fillBarcodeAndImage(ctx, pt.Id)
	return productTemplateToProto(existing, barcode, imageURL), nil
}

// GetProductTemplateByBarcode 根据条码查询商品模板及关联店铺。
func GetProductTemplateByBarcode(
	ctx context.Context,
	barcode string,
) ([]*catalogv1.GetProductTemplateByBarcodeResponse_Item, error) {
	b, err := repository.GetBarcodeByCode(ctx, barcode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrBarcodeNotFound
		}
		log.Error().Err(err).Msgf("Failed to find barcode: %s", barcode)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	pt, err := repository.GetProductTemplateByID(ctx, b.ProductTemplateID)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get product template for barcode: %s", barcode)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	store, err := repository.GetStoreByID(ctx, pt.StoreID)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get store for barcode: %s", barcode)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	imageURL, err := getFirstImageURL(ctx, pt.ID)
	if err != nil {
		log.Debug().Err(err).Msgf("Failed to get image for barcode: %s", barcode)
	}

	item := &catalogv1.GetProductTemplateByBarcodeResponse_Item{
		ProductTemplate: productTemplateToProto(pt, barcode, imageURL),
		Store:           storeToProto(store),
	}
	return []*catalogv1.GetProductTemplateByBarcodeResponse_Item{item}, nil
}

// ————— 内部辅助 —————

func fillBarcodeAndImage(ctx context.Context, ptID int64) (string, string) {
	barcode, err := getFirstBarcode(ctx, ptID)
	if err != nil {
		log.Debug().Err(err).Msgf("Failed to get barcode for product template: %d", ptID)
	}
	imageURL, err := getFirstImageURL(ctx, ptID)
	if err != nil {
		log.Debug().Err(err).Msgf("Failed to get image for product template: %d", ptID)
	}
	return barcode, imageURL
}

func getFirstBarcode(ctx context.Context, ptID int64) (string, error) {
	b, err := repository.GetBarcodeByProductTemplateID(ctx, ptID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return b.Barcode, nil
}

func getFirstImageURL(ctx context.Context, ptID int64) (string, error) {
	img, err := repository.GetImageByProductTemplateID(ctx, ptID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return img.ImageURL, nil
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

func buildProductTemplateUpdates(pt *catalogv1.ProductTemplate, maskPaths []string) map[string]any {
	updates := make(map[string]any)
	for _, path := range maskPaths {
		switch path {
		case "title":
			updates["title"] = pt.Title
		case "description":
			updates["description"] = pt.Description
		case "price_cents":
			updates["price_cents"] = pt.PriceCents
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

func applyProductTemplateUpdates(pt *model.CatalogProductTemplate, updates map[string]any) {
	if v, ok := updates["title"]; ok {
		if s, ok2 := v.(string); ok2 {
			pt.Title = s
		}
	}
	if v, ok := updates["description"]; ok {
		if s, ok2 := v.(string); ok2 {
			pt.Description = s
		}
	}
	if v, ok := updates["price_cents"]; ok {
		if n, ok2 := v.(int32); ok2 {
			pt.PriceCents = n
		}
	}
}
