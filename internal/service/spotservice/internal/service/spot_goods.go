package service

import (
	"context"
	"math"
	"time"

	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/repository"
	"github.com/rs/zerolog/log"
)

func ListSpotGoods(ctx context.Context, storeID int64, offset, limit int) ([]*model.SpotGoods, error) {
	spotGoodsList, err := repository.ListSpotGoods(ctx, storeID, offset, limit)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to list spot goods for storeID: %d", storeID)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	return spotGoodsList, nil
}

func GetSpotGoodLength(ctx context.Context, storeID int64) (int32, error) {
	count, err := repository.GetSpotGoodsLength(ctx, storeID)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get spot goods length for storeID: %d", storeID)
		return 0, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	if count > math.MaxInt32 || count < 0 {
		log.Warn().
			Msgf("Spot goods count exceeds int32 limit for storeID: %d, returning -1 to indicate overflow", storeID)
		return -1, nil
	}
	return int32(count), nil
}

func GetSpotGoods(ctx context.Context, goodsID int64) (*model.SpotGoods, error) {
	goods, err := repository.GetSpotGoodsByID(ctx, goodsID)
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
	spotGoodsList, err := repository.GetSpotGoodsByIDs(ctx, goodsIDs)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get spot goods by IDs: %v", goodsIDs)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	return spotGoodsList, nil
}

func CreateSpotGoods(ctx context.Context, goods *model.SpotGoods) error {
	err := repository.CreateSpotGoods(ctx, goods)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to create spot good: %v", goods)
		return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	return nil
}

func UpdateSpotGoodsStock(ctx context.Context, goodsID int64, newStockTotal int32, updatedAt time.Time) error {
	goods, err := repository.GetSpotGoodsByID(ctx, goodsID)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get spot good info for goodsID: %d before updating stock", goodsID)
		return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	if goods == nil || goods.ClosedAt != nil {
		log.Warn().Msgf("Spot good not found for goodsID: %d when updating stock", goodsID)
		return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	err = repository.UpdateSpotGoodsStock(ctx, goodsID, newStockTotal, updatedAt)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to update spot good stock total for goodsID: %d", goodsID)
		return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	return nil
}

func UpdateSpotGoodsPrice(ctx context.Context, goodsID int64, newSalePriceCents int32, updatedAt time.Time) error {
	goods, err := repository.GetSpotGoodsByID(ctx, goodsID)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get spot good info for goodsID: %d before updating price", goodsID)
		return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	if goods == nil || goods.ClosedAt != nil {
		log.Warn().Msgf("Spot good not found for goodsID: %d when updating price", goodsID)
		return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	err = repository.UpdateSpotGoodsPrice(ctx, goodsID, newSalePriceCents, updatedAt)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to update spot good sale price for goodsID: %d", goodsID)
		return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	return nil
}
