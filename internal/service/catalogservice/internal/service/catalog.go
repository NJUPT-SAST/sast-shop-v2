package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/errmsg"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/repository"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// 哨兵错误
var (
	ErrStoreNotFound            = errors.New("store not found")
	ErrProductNotFound          = errors.New("product template not found")
	ErrBarcodeNotFound          = errors.New("barcode not found")
	ErrProductTemplateForbidden = errors.New("product template forbidden")
)

// catalogInternalError 返回 catalog 服务的内部错误。
func catalogInternalError() error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
		CatalogError: &catalogv1.CatalogError{
			Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
		},
	}, "")
}

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
	if id <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errmsg.InvalidArgument)
	}
	pt, err := repository.GetProductTemplateByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, errmsg.ProductTemplateNotFound)
	}
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
// 整个读-改-写在一个数据库事务中完成，读取时使用 SELECT ... FOR UPDATE 防止并发写冲突。
func UpdateStore(
	ctx context.Context,
	store *catalogv1.Store,
	updateMask []string,
) (*catalogv1.Store, error) {
	if store == nil || store.Id <= 0 {
		return nil, connect.NewError(errmsg.InvalidArgument.Code, errmsg.InvalidArgument)
	}
	tx, err := postgres.DB.BeginTx(ctx, nil)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to begin transaction for store update: %d", store.Id)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			log.Debug().Err(err).Msg("rollback after commit, expected")
		}
	}()

	existing, err := repository.GetStoreByIDForUpdate(ctx, tx, store.Id)
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

	if err := repository.UpdateStore(ctx, tx, store.Id, updates); err != nil {
		log.Error().Err(err).Msgf("Failed to update store: %d", store.Id)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	if err := tx.Commit(); err != nil {
		log.Error().Err(err).Msgf("Failed to commit transaction for store update: %d", store.Id)
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
	keyword string,
) ([]*catalogv1.ProductTemplate, int32, error) {
	if storeID < 0 || page <= 0 || pageSize <= 0 || pageSize > 100 {
		return nil, 0, connect.NewError(errmsg.InvalidArgument.Code, errmsg.InvalidArgument)
	}
	total, err := repository.CountProductTemplates(ctx, storeID, keyword)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to count product templates for store: %d", storeID)
		return nil, 0, rpcerror.NewInternalError(&commonv1.BusinessError_CatalogError{
			CatalogError: &catalogv1.CatalogError{
				Code: catalogv1.CatalogErrorCode_CATALOG_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	offset := (int(page) - 1) * int(pageSize)
	pts, err := repository.ListProductTemplates(ctx, storeID, offset, int(pageSize), keyword)
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
	if storeID <= 0 || createdByUserID <= 0 || strings.TrimSpace(title) == "" || priceCents <= 0 ||
		strings.TrimSpace(barcode) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errmsg.InvalidArgument)
	}
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
	err = postgres.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := repository.CreateProductTemplate(ctx, tx, pt); err != nil {
			return err
		}
		if barcode != "" {
			b := &model.CatalogProductBarcode{ProductTemplateID: pt.ID, Barcode: barcode}
			if err := repository.CreateBarcode(ctx, tx, b); err != nil {
				return err
			}
		}
		if mainImageURL != "" {
			img := &model.CatalogProductImage{ProductTemplateID: pt.ID, ImageURL: mainImageURL, SortOrder: 0}
			if err := repository.CreateImage(ctx, tx, img); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to create product template with barcode and image")
		return nil, catalogInternalError()
	}

	return productTemplateToProto(pt, barcode, mainImageURL), nil
}

// UpdateProductTemplate 部分更新商品模板。
func UpdateProductTemplate(
	ctx context.Context,
	pt *catalogv1.ProductTemplate,
	updateMask []string,
) (*catalogv1.ProductTemplate, error) {
	if pt == nil || pt.Id <= 0 || pt.UpdatedAt == nil || !pt.UpdatedAt.IsValid() {
		return nil, connect.NewError(errmsg.InvalidArgument.Code, errmsg.InvalidArgument)
	}
	existing, err := getProductTemplateForUpdate(ctx, pt.Id)
	if err != nil {
		return nil, err
	}
	if err := validateProductTemplateUpdate(ctx, pt, updateMask); err != nil {
		return nil, err
	}

	updates := buildProductTemplateUpdates(pt, updateMask)
	updateBarcode, updateImage := splitProductTemplateMask(updateMask)

	if len(updates) == 0 && !updateBarcode && !updateImage {
		barcode, imageURL := fillBarcodeAndImage(ctx, pt.Id)
		return productTemplateToProto(existing, barcode, imageURL), nil
	}

	if err := applyProductTemplateUpdatesTx(ctx, pt, updates, updateBarcode, updateImage); err != nil {
		return nil, err
	}

	existing, err = getProductTemplateForUpdate(ctx, pt.Id)
	if err != nil {
		return nil, err
	}
	barcode, imageURL := fillBarcodeAndImage(ctx, pt.Id)
	return productTemplateToProto(existing, barcode, imageURL), nil
}

func validateProductTemplateUpdate(ctx context.Context, pt *catalogv1.ProductTemplate, updateMask []string) error {
	for _, field := range updateMask {
		switch field {
		case "title":
			if strings.TrimSpace(pt.Title) == "" {
				return connect.NewError(connect.CodeInvalidArgument, errmsg.InvalidArgument)
			}
		case "price_cents":
			if pt.PriceCents <= 0 {
				return connect.NewError(connect.CodeInvalidArgument, errmsg.InvalidArgument)
			}
		case "store_id":
			if pt.StoreId <= 0 {
				return connect.NewError(connect.CodeInvalidArgument, errmsg.InvalidArgument)
			}
			if _, err := repository.GetStoreByID(ctx, pt.StoreId); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrStoreNotFound
				}
				return catalogInternalError()
			}
		case "barcode":
			if strings.TrimSpace(pt.Barcode) == "" {
				return connect.NewError(connect.CodeInvalidArgument, errmsg.InvalidArgument)
			}
		case "description", "main_image_url":
		default:
			return connect.NewError(connect.CodeInvalidArgument, errmsg.InvalidArgument)
		}
	}
	return nil
}

func getProductTemplateForUpdate(ctx context.Context, id int64) (*model.CatalogProductTemplate, error) {
	existing, err := repository.GetProductTemplateByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrProductNotFound
		}
		log.Error().Err(err).Msgf("Failed to get product template for update: %d", id)
		return nil, catalogInternalError()
	}
	return existing, nil
}

func splitProductTemplateMask(updateMask []string) (updateBarcode, updateImage bool) {
	for _, path := range updateMask {
		switch path {
		case "barcode":
			updateBarcode = true
		case "main_image_url":
			updateImage = true
		}
	}
	return updateBarcode, updateImage
}

func applyProductTemplateUpdatesTx(
	ctx context.Context,
	pt *catalogv1.ProductTemplate,
	updates map[string]any,
	updateBarcode, updateImage bool,
) error {
	tx, err := postgres.DB.BeginTx(ctx, nil)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to begin transaction for product template update: %d", pt.Id)
		return catalogInternalError()
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			log.Debug().Err(err).Msg("rollback after commit, expected")
		}
	}()

	var current model.CatalogProductTemplate
	if err := tx.NewSelect().Model(&current).Where("id = ?", pt.Id).For("UPDATE").Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrProductNotFound
		}
		return catalogInternalError()
	}
	if current.Status == model.CatalogStatusRemoved {
		return ErrProductNotFound
	}
	if !current.UpdatedAt.Equal(pt.UpdatedAt.AsTime()) {
		return connect.NewError(connect.CodeAborted, errors.New("商品模板已被修改，请刷新后重试"))
	}
	// Barcode-only and image-only edits must advance the template version too.
	updates["updated_at"] = time.Now()
	if err := repository.UpdateProductTemplate(ctx, tx, pt.Id, updates); err != nil {
		log.Error().Err(err).Msgf("Failed to update product template: %d", pt.Id)
		return catalogInternalError()
	}
	if updateBarcode {
		if err := repository.UpsertBarcodeByProductTemplateID(ctx, tx, pt.Id, pt.Barcode); err != nil {
			log.Error().Err(err).Msgf("Failed to update barcode for product template: %d", pt.Id)
			return catalogInternalError()
		}
	}
	if updateImage {
		if err := applyProductTemplateImageTx(ctx, tx, pt); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		log.Error().Err(err).Msgf("Failed to commit transaction for product template update: %d", pt.Id)
		return catalogInternalError()
	}
	return nil
}

func applyProductTemplateImageTx(
	ctx context.Context,
	tx bun.IDB,
	pt *catalogv1.ProductTemplate,
) error {
	if pt.MainImageUrl == "" {
		if err := repository.DeleteImagesByProductTemplateID(ctx, tx, pt.Id); err != nil {
			log.Error().Err(err).Msgf("Failed to delete images for product template: %d", pt.Id)
			return catalogInternalError()
		}
		return nil
	}
	if err := repository.UpsertImageByProductTemplateID(ctx, tx, pt.Id, pt.MainImageUrl); err != nil {
		log.Error().Err(err).Msgf("Failed to update image for product template: %d", pt.Id)
		return catalogInternalError()
	}
	return nil
}

// DeleteProductTemplate 软删除商品模板，仅创建者本人可删除。
func DeleteProductTemplate(ctx context.Context, id int64, callerUserID int64) error {
	pt, err := repository.GetProductTemplateByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrProductNotFound
		}
		log.Error().Err(err).Msgf("Failed to get product template for delete: %d", id)
		return catalogInternalError()
	}
	if pt.CreatedByUserID != callerUserID {
		return ErrProductTemplateForbidden
	}
	if err := repository.SoftDeleteProductTemplate(ctx, id); err != nil {
		log.Error().Err(err).Msgf("Failed to soft delete product template: %d", id)
		return catalogInternalError()
	}
	return nil
}

// GetProductTemplateByBarcode 根据条码查询商品模板及关联店铺。
func GetProductTemplateByBarcode(
	ctx context.Context,
	barcode string,
) ([]*catalogv1.GetProductTemplateByBarcodeResponse_Item, error) {
	if strings.TrimSpace(barcode) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errmsg.InvalidArgument)
	}
	templates, err := repository.ListProductTemplatesByBarcode(ctx, strings.TrimSpace(barcode))
	if err != nil {
		log.Error().Err(err).Msg("Failed to find product templates by barcode")
		return nil, catalogInternalError()
	}
	if len(templates) == 0 {
		return nil, ErrBarcodeNotFound
	}
	items := make([]*catalogv1.GetProductTemplateByBarcodeResponse_Item, 0, len(templates))
	for _, pt := range templates {
		store, err := repository.GetStoreByID(ctx, pt.StoreID)
		if err != nil {
			return nil, catalogInternalError()
		}
		imageURL, err := getFirstImageURL(ctx, pt.ID)
		if err != nil {
			return nil, catalogInternalError()
		}
		items = append(items, &catalogv1.GetProductTemplateByBarcodeResponse_Item{
			ProductTemplate: productTemplateToProto(pt, strings.TrimSpace(barcode), imageURL),
			Store:           storeToProto(store),
		})
	}
	return items, nil
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
		case "store_id":
			updates["store_id"] = pt.StoreId
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
