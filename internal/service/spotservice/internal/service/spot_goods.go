package service

import (
	"context"

	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/repository"
	"github.com/rs/zerolog/log"
)

func GetSpotGoodInfo(ctx context.Context, goodsID int64) (*model.SpotGoods, error) {
	goods, err := repository.GetSpotGoodByID(ctx, goodsID)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get spot good info for goodsID: %d", goodsID)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	return goods, nil
}

func GetSpotGoodsByIDs(ctx context.Context, goodsIDs []int64) ([]*model.SpotGoods, error) {
	return repository.GetSpotGoodsByIDs(ctx, goodsIDs)
}

func ListSpotGoods(ctx context.Context, offset, limit int) ([]*model.SpotGoods, error) {
	return repository.ListSpotGoods(ctx, offset, limit)
}

func CreateSpotGoods(ctx context.Context, goods *model.SpotGoods) error {
	return repository.CreateSpotGoods(ctx, goods)
}

func UpdateSpotGoodsStockTotal(ctx context.Context, goodsID int64, newStockTotal int32) error {
	return repository.UpdateSpotGoodsStockTotal(ctx, goodsID, newStockTotal)
}

func UpdateSpotGoodsSalePriceCents(ctx context.Context, goodsID int64, newSalePriceCents int32) error {
	return repository.UpdateSpotGoodsSalePriceCents(ctx, goodsID, newSalePriceCents)
}

func DeleteSpotGoods(ctx context.Context, goodsID int64) error {
	return repository.DeleteSpotGoods(ctx, goodsID)
}
