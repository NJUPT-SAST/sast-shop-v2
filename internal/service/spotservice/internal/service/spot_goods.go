package service

import (
	"context"
	"errors"
	"math"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/client"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/repository"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func ListSpotGoods(ctx context.Context, storeID int64, offset, limit int) ([]*spotv1.SpotGoodsBrief, error) {
	spotGoodsList, err := repository.ListSpotGoods(ctx, storeID, offset, limit)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to list spot goods for storeID: %d", storeID)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	briefs := make([]*spotv1.SpotGoodsBrief, 0, len(spotGoodsList))
	for _, g := range spotGoodsList {
		briefs = append(briefs, &spotv1.SpotGoodsBrief{
			Id:             g.ID,
			SalePriceCents: g.SalePriceCents,
			CreatedAt:      timestamppb.New(g.CreatedAt),
			UpdatedAt:      timestamppb.New(g.UpdatedAt),
		})
	}
	return briefs, nil
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
		log.Error().Msgf("Spot goods count out of int32 range for storeID: %d (count=%d)", storeID, count)
		return 0, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "spot goods count out of range")
	}
	return int32(count), nil
}

func GetSpotGoods(ctx context.Context, goodsID int64) (*spotv1.SpotGoodsDetail, error) {
	goods, err := repository.GetSpotGoodsByID(ctx, goodsID)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get spot good info for goodsID: %d", goodsID)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	return modelToDetail(goods), nil
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

// ValidateProductTemplate checks if the product template exists and if its updated_at timestamp matches the provided one.
// If the template does not exist or the timestamps do not match, it returns an error.
func ValidateProductTemplate(
	ctx context.Context,
	productTemplateID int64,
	productTemplateUpdatedAt *timestamppb.Timestamp,
) (*catalogv1.ProductTemplate, error) {
	resp, err := client.CatalogInternalServiceClient.GetProductTemplate(
		ctx,
		connect.NewRequest(&catalogv1.GetProductTemplateRequest{
			ProductTemplateId: productTemplateID,
		}),
	)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get product template for templateID: %d", productTemplateID)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "failed to get product template")
	}
	template := resp.Msg.GetProductTemplate()
	if template == nil {
		log.Warn().Msgf("Product template not found for templateID: %d", productTemplateID)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "product template not found")
	}
	if productTemplateUpdatedAt == nil || !template.GetUpdatedAt().AsTime().Equal(productTemplateUpdatedAt.AsTime()) {
		log.Warn().Msgf("Product template updated_at mismatch for templateID: %d", productTemplateID)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "product template has been updated, please refresh the page")
	}
	return template, nil
}

func CreateSpotGoodsTx(ctx context.Context, goods *model.SpotGoods) error {
	return postgres.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := repository.CreateSpotGoodsTx(ctx, tx, goods); err != nil {
			return err
		}
		ledger := &model.SpotStockLedger{
			ListingID:  goods.ID,
			Delta:      goods.StockTotal,
			Reason:     model.StockLedgerReasonPublish,
			OperatorID: &goods.SellerID,
		}
		return repository.CreateStockLedger(ctx, tx, ledger)
	})
}

func CreateSpotGoods(
	ctx context.Context,
	goods *model.SpotGoods,
	productTemplateUpdatedAt *timestamppb.Timestamp,
) (*spotv1.SpotGoodsDetail, error) {
	template, err := ValidateProductTemplate(ctx, goods.ProductTemplateID, productTemplateUpdatedAt)
	if err != nil {
		return nil, err
	}

	goods.StoreID = template.GetStoreId()

	if err := CreateSpotGoodsTx(ctx, goods); err != nil {
		log.Error().Err(err).Msgf("Failed to create spot good: %v", goods)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	return modelToDetail(goods), nil
}

func UpdateSpotGoodsStock(
	ctx context.Context,
	callerID int64,
	goodsID int64,
	newStockTotal int32,
	updatedAt *timestamppb.Timestamp,
) error {
	goods, err := repository.GetSpotGoodsByID(ctx, goodsID)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get spot good info for goodsID: %d before updating stock", goodsID)
		return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	if goods.ClosedAt != nil {
		log.Warn().Msgf("Spot good is closed for goodsID: %d, cannot update stock", goodsID)
		return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	if goods.SellerID != callerID {
		log.Warn().Msgf("Caller %d is not the seller of goodsID: %d, cannot update stock", callerID, goodsID)
		return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	if updatedAt == nil {
		log.Warn().Msgf("UpdatedAt is nil in update stock request for goodsID: %d", goodsID)
		return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	oldStock := goods.StockTotal
	delta := newStockTotal - oldStock

	err = postgres.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		rows, err := repository.UpdateSpotGoodsStockTx(ctx, tx, goodsID, newStockTotal, updatedAt.AsTime())
		if err != nil {
			return err
		}
		if rows == 0 {
			return errors.New("goods was modified by another request")
		}
		ledger := &model.SpotStockLedger{
			ListingID:  goodsID,
			Delta:      delta,
			Reason:     model.StockLedgerReasonManualAdjust,
			OperatorID: &callerID,
		}
		return repository.CreateStockLedger(ctx, tx, ledger)
	})
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

func UpdateSpotGoodsPrice(
	ctx context.Context,
	callerID int64,
	goodsID int64,
	newSalePriceCents int32,
	updatedAt *timestamppb.Timestamp,
) error {
	goods, err := repository.GetSpotGoodsByID(ctx, goodsID)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get spot good info for goodsID: %d before updating price", goodsID)
		return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	if goods.ClosedAt != nil {
		log.Warn().Msgf("Spot good is closed for goodsID: %d, cannot update price", goodsID)
		return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	if goods.SellerID != callerID {
		log.Warn().Msgf("Caller %d is not the seller of goodsID: %d, cannot update price", callerID, goodsID)
		return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	if updatedAt == nil {
		log.Warn().Msgf("UpdatedAt is nil in update price request for goodsID: %d", goodsID)
		return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	rows, err := repository.UpdateSpotGoodsPrice(ctx, goodsID, newSalePriceCents, updatedAt.AsTime())
	if err != nil {
		log.Error().Err(err).Msgf("Failed to update spot good sale price for goodsID: %d", goodsID)
		return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	if rows == 0 {
		log.Warn().Msgf("Optimistic lock conflict when updating price for goodsID: %d", goodsID)
		return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "optimistic lock conflict")
	}
	return nil
}

func modelToDetail(goods *model.SpotGoods) *spotv1.SpotGoodsDetail {
	return &spotv1.SpotGoodsDetail{
		Id:             goods.ID,
		SalePriceCents: goods.SalePriceCents,
		CreatedAt:      timestamppb.New(goods.CreatedAt),
		UpdatedAt:      timestamppb.New(goods.UpdatedAt),
		Stock:          goods.StockTotal,
	}
}
