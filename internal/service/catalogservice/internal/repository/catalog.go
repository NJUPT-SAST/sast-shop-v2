package repository

import (
	"context"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/model"
)

// GetProductTemplateByID 按 ID 查询商品模板。
func GetProductTemplateByID(ctx context.Context, id int64) (*model.CatalogProductTemplate, error) {
	var pt model.CatalogProductTemplate
	err := postgres.DB.NewSelect().Model(&pt).Where("id = ?", id).Scan(ctx)
	return &pt, err
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
