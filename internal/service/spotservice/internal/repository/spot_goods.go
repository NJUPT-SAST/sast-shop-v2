package repository

import (
	"context"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"github.com/uptrace/bun"
)

func ListSpotGoods(ctx context.Context, offset, limit int) ([]*model.SpotGoods, error) {
	var goodsList []*model.SpotGoods
	err := postgres.DB.NewSelect().Model(&goodsList).Offset(offset).Limit(limit).Scan(ctx)
	return goodsList, err
}

func GetSpotGoodByID(ctx context.Context, goodsID int64) (*model.SpotGoods, error) {
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
	_, err := postgres.DB.NewInsert().Model(goods).Exec(ctx)
	return err
}

func UpdateSpotGoodsStockTotal(ctx context.Context, goodsID int64, newStockTotal int32) error {
	_, err := postgres.DB.NewUpdate().
		Model((*model.SpotGoods)(nil)).
		Set("stock = ?", newStockTotal).
		Where("id = ?", goodsID).
		Exec(ctx)
	return err
}

func UpdateSpotGoodsSalePriceCents(ctx context.Context, goodsID int64, newSalePriceCents int32) error {
	_, err := postgres.DB.NewUpdate().
		Model((*model.SpotGoods)(nil)).
		Set("price = ?", newSalePriceCents).
		Where("id = ?", goodsID).
		Exec(ctx)
	return err
}

func DeleteSpotGoods(ctx context.Context, goodsID int64) error {
	_, err := postgres.DB.NewUpdate().
		Model((*model.SpotGoods)(nil)).
		Set("closed_at = now()").
		Where("id = ?", goodsID).
		Exec(ctx)
	return err
}
