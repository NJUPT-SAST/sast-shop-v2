package repository

import (
	"context"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/model"
	"github.com/uptrace/bun"
)

// GetProductTemplateByID 按 ID 查询商品模板。
func GetProductTemplateByID(ctx context.Context, id int64) (*model.CatalogProductTemplate, error) {
	var pt model.CatalogProductTemplate
	err := postgres.DB.NewSelect().Model(&pt).Where("id = ?", id).Scan(ctx)
	return &pt, err
}

// ListProductTemplatesByIDs需要通过商品id渲染商品模板
func ListProductTemplatesByIDs(ctx context.Context, ids []int64) ([]*model.CatalogProductTemplate, error) {
	if len(ids) == 0 {
		return []*model.CatalogProductTemplate{}, nil
	}

	var pts []*model.CatalogProductTemplate
	err := postgres.DB.NewSelect().
		Model(&pts).
		Where("id IN (?)", bun.List(ids)).
		Order("id ASC").
		Scan(ctx)
	return pts, err
}

// GetStoreByID 按 ID 查询店铺。
func GetStoreByID(ctx context.Context, id int64) (*model.CatalogStore, error) {
	var store model.CatalogStore
	err := postgres.DB.NewSelect().Model(&store).Where("id = ?", id).Scan(ctx)
	return &store, err
}

// ListStores 查询所有店铺。
func ListStores(ctx context.Context) ([]*model.CatalogStore, error) {
	var stores []*model.CatalogStore
	err := postgres.DB.NewSelect().Model(&stores).Scan(ctx)
	return stores, err
}

// CreateStore 创建店铺。
func CreateStore(ctx context.Context, store *model.CatalogStore) error {
	_, err := postgres.DB.NewInsert().Model(store).Exec(ctx)
	return err
}

// UpdateStore 部分更新店铺，updates 为需要更新的列名→值映射。
func UpdateStore(ctx context.Context, id int64, updates map[string]any) error {
	_, err := postgres.DB.NewUpdate().
		Model(&updates).
		TableExpr("catalog.catalog_store").
		Where("id = ?", id).
		Exec(ctx)
	return err
}

// ————— 商品模板 —————

// ListProductTemplates 分页查询指定店铺的商品模板。
func ListProductTemplates(
	ctx context.Context, storeID int64, offset, limit int,
) ([]*model.CatalogProductTemplate, error) {
	var pts []*model.CatalogProductTemplate
	err := postgres.DB.NewSelect().
		Model(&pts).
		Where("store_id = ?", storeID).
		Order("id DESC").
		Limit(limit).
		Offset(offset).
		Scan(ctx)
	return pts, err
}

// CountProductTemplates 统计指定店铺的商品模板总数。
func CountProductTemplates(ctx context.Context, storeID int64) (int, error) {
	return postgres.DB.NewSelect().
		Model((*model.CatalogProductTemplate)(nil)).
		Where("store_id = ?", storeID).
		Count(ctx)
}

// CreateProductTemplate 创建商品模板。
func CreateProductTemplate(ctx context.Context, pt *model.CatalogProductTemplate) error {
	_, err := postgres.DB.NewInsert().Model(pt).Exec(ctx)
	return err
}

// CreateBarcode 创建商品条码记录。
func CreateBarcode(ctx context.Context, barcode *model.CatalogProductBarcode) error {
	_, err := postgres.DB.NewInsert().Model(barcode).Exec(ctx)
	return err
}

// CreateImage 创建商品图片记录。
func CreateImage(ctx context.Context, image *model.CatalogProductImage) error {
	_, err := postgres.DB.NewInsert().Model(image).Exec(ctx)
	return err
}

// UpdateProductTemplate 部分更新商品模板，updates 为需要更新的列名→值映射。
func UpdateProductTemplate(ctx context.Context, id int64, updates map[string]any) error {
	_, err := postgres.DB.NewUpdate().
		Model(&updates).
		TableExpr("catalog.catalog_product_template").
		Where("id = ?", id).
		Exec(ctx)
	return err
}

// GetBarcodeByCode 按条码号查询条码记录。
func GetBarcodeByCode(ctx context.Context, barcode string) (*model.CatalogProductBarcode, error) {
	var b model.CatalogProductBarcode
	err := postgres.DB.NewSelect().Model(&b).Where("barcode = ?", barcode).Scan(ctx)
	return &b, err
}

// GetBarcodeByProductTemplateID 获取商品模板的第一个条码。
func GetBarcodeByProductTemplateID(ctx context.Context, ptID int64) (*model.CatalogProductBarcode, error) {
	var b model.CatalogProductBarcode
	err := postgres.DB.NewSelect().
		Model(&b).
		Where("product_template_id = ?", ptID).
		Limit(1).
		Scan(ctx)
	return &b, err
}

// GetImageByProductTemplateID 获取商品模板的第一张图片。
func GetImageByProductTemplateID(ctx context.Context, ptID int64) (*model.CatalogProductImage, error) {
	var img model.CatalogProductImage
	err := postgres.DB.NewSelect().
		Model(&img).
		Where("product_template_id = ?", ptID).
		Order("sort_order ASC").
		Limit(1).
		Scan(ctx)
	return &img, err
}
