package repository

import (
	"context"
	"errors"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/model"
	"github.com/uptrace/bun"
)

func ListProductCategories(ctx context.Context, storeID int64) ([]*model.CatalogProductCategory, error) {
	var categories []*model.CatalogProductCategory
	err := postgres.DB.NewSelect().Model(&categories).
		Where("cpc.store_id = ?", storeID).
		OrderExpr("cpc.parent_id ASC NULLS FIRST, cpc.sort_order ASC, cpc.id ASC").
		Scan(ctx)
	return categories, err
}

func HasVisibleUncategorizedProducts(ctx context.Context, storeID int64) (bool, error) {
	return postgres.DB.NewSelect().TableExpr("catalog.catalog_product_template AS cpt").
		Where("cpt.store_id = ?", storeID).
		Where("cpt.category_id IS NULL").
		Where("cpt.status != ?", model.CatalogStatusRemoved).
		Where("cpt.moderation_status = ?", "allowed").
		Where("EXISTS (SELECT 1 FROM catalog.catalog_store cs WHERE cs.id = cpt.store_id AND cs.moderation_status = 'allowed' AND cs.status != 'removed')").
		Exists(ctx)
}

func GetProductCategory(
	ctx context.Context,
	db bun.IDB,
	storeID, categoryID int64,
) (*model.CatalogProductCategory, error) {
	var category model.CatalogProductCategory
	err := db.NewSelect().Model(&category).
		Where("cpc.store_id = ?", storeID).
		Where("cpc.id = ?", categoryID).
		Scan(ctx)
	return &category, err
}

func GetProductCategoryForUpdate(
	ctx context.Context,
	db bun.IDB,
	storeID, categoryID int64,
) (*model.CatalogProductCategory, error) {
	var category model.CatalogProductCategory
	err := db.NewSelect().Model(&category).
		Where("cpc.store_id = ?", storeID).
		Where("cpc.id = ?", categoryID).
		For("UPDATE").
		Scan(ctx)
	return &category, err
}

func HasDirectProductTemplates(ctx context.Context, db bun.IDB, storeID, categoryID int64) (bool, error) {
	return db.NewSelect().TableExpr("catalog.catalog_product_template AS cpt").
		Where("cpt.store_id = ?", storeID).
		Where("cpt.category_id = ?", categoryID).
		Where("cpt.status != ?", model.CatalogStatusRemoved).
		Exists(ctx)
}

func ValidateProductTemplateCategory(ctx context.Context, db bun.IDB, storeID, categoryID int64) error {
	category, err := GetProductCategory(ctx, db, storeID, categoryID)
	if err != nil {
		return err
	}
	if category.ParentID != nil {
		return nil
	}
	hasChildren, err := HasChildProductCategories(ctx, db, storeID, categoryID)
	if err != nil {
		return err
	}
	if hasChildren {
		return errors.New("一级分类有子分类时不能直接关联商品")
	}
	return nil
}

func HasChildProductCategories(ctx context.Context, db bun.IDB, storeID, categoryID int64) (bool, error) {
	return db.NewSelect().TableExpr("catalog.catalog_product_category AS cpc").
		Where("cpc.store_id = ?", storeID).
		Where("cpc.parent_id = ?", categoryID).
		Exists(ctx)
}

func NextProductCategorySortOrder(ctx context.Context, db bun.IDB, storeID int64, parentID *int64) (int32, error) {
	query := db.NewSelect().TableExpr("catalog.catalog_product_category AS cpc").
		ColumnExpr("COALESCE(MAX(cpc.sort_order), -1) + 1").
		Where("cpc.store_id = ?", storeID)
	if parentID == nil {
		query = query.Where("cpc.parent_id IS NULL")
	} else {
		query = query.Where("cpc.parent_id = ?", *parentID)
	}
	var sortOrder int32
	err := query.Scan(ctx, &sortOrder)
	return sortOrder, err
}

func CreateProductCategory(ctx context.Context, db bun.IDB, category *model.CatalogProductCategory) error {
	_, err := db.NewInsert().Model(category).Exec(ctx)
	return err
}

func ClearRemovedTemplateCategory(ctx context.Context, db bun.IDB, storeID, categoryID int64) error {
	_, err := db.NewUpdate().TableExpr("catalog.catalog_product_template").
		Set("category_id = NULL").
		Where("store_id = ?", storeID).
		Where("category_id = ?", categoryID).
		Where("status = ?", model.CatalogStatusRemoved).
		Exec(ctx)
	return err
}

func DeleteProductCategory(ctx context.Context, db bun.IDB, storeID, categoryID int64) error {
	_, err := db.NewDelete().TableExpr("catalog.catalog_product_category").
		Where("store_id = ?", storeID).
		Where("id = ?", categoryID).
		Exec(ctx)
	return err
}
