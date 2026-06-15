package repository

import (
	"context"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/model"
)

func GetProductTemplateByID(ctx context.Context, id int64) (*model.CatalogProductTemplate, error) {
	var pt model.CatalogProductTemplate
	err := postgres.DB.NewSelect().Model(&pt).Where("id = ?", id).Scan(ctx)
	return &pt, err
}

func GetStoreByID(ctx context.Context, id int64) (*model.CatalogStore, error) {
	var store model.CatalogStore
	err := postgres.DB.NewSelect().Model(&store).Where("id = ?", id).Scan(ctx)
	return &store, err
}
