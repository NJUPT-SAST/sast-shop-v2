package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/repository"
	"github.com/uptrace/bun"
)

var (
	ErrProductCategoryNotFound          = errors.New("product category not found")
	ErrProductCategoryInUse             = errors.New("product category has product templates")
	ErrProductCategoryHasChildren       = errors.New("product category has child categories")
	ErrProductCategoryParentHasProducts = errors.New("cannot add a child category while the parent has products")
)

func normalizeProductCategoryName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 50 {
		return "", errors.New("分类名称必须为 1 到 50 个字符")
	}
	return name, nil
}

func validateTemplateHasCategory(current *model.CatalogProductTemplate, updates map[string]any) error {
	if current.CategoryID == nil {
		if _, assigningCategory := updates["category_id"]; !assigningCategory {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("商品尚未分类，请选择有效分类后再保存"))
		}
	}
	return nil
}

func validateTemplateStoreMatchesLockedStore(lockedStoreID, currentStoreID int64) error {
	if currentStoreID != lockedStoreID {
		return connect.NewError(connect.CodeAborted, errors.New("商品模板店铺已变更，请重试"))
	}
	return nil
}

func productCategoryToProto(category *model.CatalogProductCategory) *catalogv1.ProductCategory {
	result := &catalogv1.ProductCategory{
		Id: category.ID, StoreId: category.StoreID, Name: category.Name, SortOrder: category.SortOrder,
	}
	if category.ParentID != nil {
		result.ParentId = category.ParentID
	}
	return result
}

func ListProductCategories(ctx context.Context, storeID int64) ([]*catalogv1.ProductCategory, bool, error) {
	if storeID <= 0 {
		return nil, false, connect.NewError(connect.CodeInvalidArgument, errors.New("店铺 ID 无效"))
	}
	store, err := repository.GetStoreByID(ctx, storeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, ErrStoreNotFound
		}
		return nil, false, catalogInternalError()
	}
	if store.ModerationStatus == "blocked" || store.Status == model.CatalogStatusRemoved {
		return nil, false, connect.NewError(connect.CodeFailedPrecondition, errors.New("店铺当前不可用"))
	}
	categories, err := repository.ListProductCategories(ctx, storeID)
	if err != nil {
		return nil, false, catalogInternalError()
	}
	hasUncategorized, err := repository.HasVisibleUncategorizedProducts(ctx, storeID)
	if err != nil {
		return nil, false, catalogInternalError()
	}
	result := make([]*catalogv1.ProductCategory, 0, len(categories))
	for _, category := range categories {
		result = append(result, productCategoryToProto(category))
	}
	return result, hasUncategorized, nil
}

func CreateProductCategory(
	ctx context.Context,
	storeID int64,
	parentID *int64,
	name string,
) (*catalogv1.ProductCategory, error) {
	if storeID <= 0 || (parentID != nil && *parentID <= 0) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("分类参数无效"))
	}
	name, err := normalizeProductCategoryName(name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	category := &model.CatalogProductCategory{StoreID: storeID, ParentID: parentID, Name: name}
	err = postgres.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockProductCategoryStore(ctx, tx, storeID); err != nil {
			return err
		}
		if err := requireStoreAvailable(ctx, tx, storeID); err != nil {
			return err
		}
		if parentID != nil {
			if err := validateCategoryParent(ctx, tx, storeID, *parentID); err != nil {
				return err
			}
		}
		sortOrder, err := repository.NextProductCategorySortOrder(ctx, tx, storeID, parentID)
		if err != nil {
			return err
		}
		category.SortOrder = sortOrder
		return repository.CreateProductCategory(ctx, tx, category)
	})
	if err != nil {
		return nil, mapCreateCategoryError(err)
	}
	return productCategoryToProto(category), nil
}

func mapCreateCategoryError(err error) error {
	if strings.Contains(err.Error(), "catalog_product_category_root_name_key") ||
		strings.Contains(err.Error(), "catalog_product_category_child_name_key") {
		return connect.NewError(connect.CodeAlreadyExists, errors.New("同级分类已存在同名分类"))
	}
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return err
	}
	if errors.Is(err, ErrProductCategoryParentHasProducts) {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("该一级分类已有商品，暂不能添加二级分类"))
	}
	if errors.Is(err, ErrProductCategoryNotFound) {
		return connect.NewError(connect.CodeNotFound, errors.New("商品分类不存在"))
	}
	if errors.Is(err, ErrStoreNotFound) {
		return ErrStoreNotFound
	}
	return catalogInternalError()
}

func DeleteProductCategory(ctx context.Context, storeID, categoryID int64) error {
	if storeID <= 0 || categoryID <= 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("分类参数无效"))
	}
	err := postgres.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockProductCategoryStore(ctx, tx, storeID); err != nil {
			return err
		}
		if err := requireStoreAvailable(ctx, tx, storeID); err != nil {
			return err
		}
		if err := validateCategoryDeletion(ctx, tx, storeID, categoryID); err != nil {
			return err
		}
		if err := repository.ClearRemovedTemplateCategory(ctx, tx, storeID, categoryID); err != nil {
			return err
		}
		return repository.DeleteProductCategory(ctx, tx, storeID, categoryID)
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return err
		}
		switch {
		case errors.Is(err, ErrStoreNotFound):
			return ErrStoreNotFound
		case errors.Is(err, ErrProductCategoryNotFound):
			return connect.NewError(connect.CodeNotFound, errors.New("商品分类不存在"))
		case errors.Is(err, ErrProductCategoryHasChildren):
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("该分类下仍有子分类"))
		case errors.Is(err, ErrProductCategoryInUse):
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("该分类下仍有未删除商品"))
		default:
			return catalogInternalError()
		}
	}
	return nil
}

func validateCategoryParent(ctx context.Context, tx bun.Tx, storeID, parentID int64) error {
	parent, err := repository.GetProductCategoryForUpdate(ctx, tx, storeID, parentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrProductCategoryNotFound
		}
		return err
	}
	if parent.ParentID != nil {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("商品分类最多支持两级"))
	}
	hasProducts, err := repository.HasDirectProductTemplates(ctx, tx, storeID, parentID)
	if err != nil {
		return err
	}
	if hasProducts {
		return ErrProductCategoryParentHasProducts
	}
	return nil
}

func validateCategoryDeletion(ctx context.Context, tx bun.Tx, storeID, categoryID int64) error {
	if _, err := repository.GetProductCategoryForUpdate(ctx, tx, storeID, categoryID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrProductCategoryNotFound
		}
		return err
	}
	hasChildren, err := repository.HasChildProductCategories(ctx, tx, storeID, categoryID)
	if err != nil {
		return err
	}
	if hasChildren {
		return ErrProductCategoryHasChildren
	}
	hasProducts, err := repository.HasDirectProductTemplates(ctx, tx, storeID, categoryID)
	if err != nil {
		return err
	}
	if hasProducts {
		return ErrProductCategoryInUse
	}
	return nil
}

func lockProductCategoryStore(ctx context.Context, tx bun.Tx, storeID int64) error {
	key := fmt.Sprintf("catalog.product_category.store:%d", storeID)
	_, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", key)
	return err
}

func lockProductCategoryStores(ctx context.Context, tx bun.Tx, storeIDs ...int64) error {
	unique := make(map[int64]struct{}, len(storeIDs))
	for _, storeID := range storeIDs {
		if storeID > 0 {
			unique[storeID] = struct{}{}
		}
	}
	ordered := make([]int64, 0, len(unique))
	for storeID := range unique {
		ordered = append(ordered, storeID)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for _, storeID := range ordered {
		if err := lockProductCategoryStore(ctx, tx, storeID); err != nil {
			return err
		}
	}
	return nil
}
