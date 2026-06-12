package repository

import (
	"context"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"github.com/uptrace/bun"
)

func ListSpotGoods(ctx context.Context, storeID int64, offset, limit int) ([]*model.SpotGoods, error) {
	var goodsList []*model.SpotGoods
	err := postgres.DB.NewSelect().
		Model(&goodsList).
		Where("store_id = ?", storeID).
		Where("closed_at IS NULL").
		Offset(offset).
		Limit(limit).
		Scan(ctx)
	return goodsList, err
}

func GetSpotGoodsLength(ctx context.Context, storeID int64) (int32, error) {
	count, err := postgres.DB.NewSelect().
		Model((*model.SpotGoods)(nil)).
		Where("store_id = ?", storeID).
		Where("closed_at IS NULL").
		Count(ctx)

	if count > 1<<31-1 {
		return -1, err
	}
	return int32(count), err
}

func GetSpotGoodsByID(ctx context.Context, goodsID int64) (*model.SpotGoods, error) {
	var goods model.SpotGoods
	err := postgres.DB.NewSelect().Model(&goods).Where("id = ?", goodsID).Scan(ctx)
	return &goods, err
}

func GetSpotGoodsByIDs(ctx context.Context, goodsIDs []int64) ([]*model.SpotGoods, error) {
	if len(goodsIDs) == 0 {
		return nil, nil
	}
	var goodsList []*model.SpotGoods
	err := postgres.DB.NewSelect().Model(&goodsList).Where("id IN (?)", bun.List(goodsIDs)).Scan(ctx)
	return goodsList, err
}

func CreateSpotGoods(ctx context.Context, goods *model.SpotGoods) error {
	if goods == nil {
		return nil
	}
	err := postgres.DB.NewInsert().Model(goods).Scan(ctx)
	return err
}

func UpdateSpotGoodsStock(ctx context.Context, goodsID int64, newStockTotal int32) error {
	err := postgres.DB.NewUpdate().
		Model((*model.SpotGoods)(nil)).
		Set("stock_total = ?", newStockTotal).
		Where("id = ?", goodsID).
		Scan(ctx)
	return err
}

func UpdateSpotGoodsPrice(ctx context.Context, goodsID int64, newSalePriceCents int32) error {
	err := postgres.DB.NewUpdate().
		Model((*model.SpotGoods)(nil)).
		Set("sale_price_cents = ?", newSalePriceCents).
		Where("id = ?", goodsID).
		Scan(ctx)
	return err
}
