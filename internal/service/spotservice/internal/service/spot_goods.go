package service

import (
	"context"
	"errors"
	"math"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/errmsg"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/timeutil"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/client"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/repository"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/proto"
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

	templates, err := getProductTemplates(ctx, spotGoodsList)
	if err != nil {
		return nil, err
	}

	briefs := make([]*spotv1.SpotGoodsBrief, 0, len(spotGoodsList))
	for _, g := range spotGoodsList {
		template := templates[g.ProductTemplateID]
		if template == nil {
			log.Error().
				Int64("spot_goods_id", g.ID).
				Int64("product_template_id", g.ProductTemplateID).
				Msg("catalog service returned no product template for spot goods")
			return nil, spotInternalError()
		}
		briefs = append(briefs, modelToBrief(g, template))
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
	if goodsID <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errmsg.InvalidArgument)
	}
	goods, err := repository.GetSpotGoodsByID(ctx, goodsID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errmsg.SpotGoodsNotFound)
	}
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get spot good info for goodsID: %d", goodsID)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	template, err := getProductTemplate(ctx, goods.ProductTemplateID)
	if err != nil {
		return nil, err
	}
	seller, err := getUser(ctx, goods.SellerID)
	if err != nil {
		return nil, err
	}
	return modelToDetail(goods, template, seller), nil
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
	if productTemplateID <= 0 || productTemplateUpdatedAt == nil || !productTemplateUpdatedAt.IsValid() {
		return nil, connect.NewError(connect.CodeInvalidArgument, errmsg.InvalidArgument)
	}
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
	if !template.GetUpdatedAt().AsTime().Equal(productTemplateUpdatedAt.AsTime()) {
		log.Warn().Msgf("Product template updated_at mismatch for templateID: %d", productTemplateID)
		return nil, connect.NewError(connect.CodeAborted, errmsg.SpotGoodsVersionConflict)
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
	if goods == nil || goods.SellerID <= 0 || goods.ProductTemplateID <= 0 || goods.SalePriceCents <= 0 ||
		goods.StockTotal <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errmsg.InvalidArgument)
	}
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
	seller, err := getUser(ctx, goods.SellerID)
	if err != nil {
		return nil, err
	}
	return modelToDetail(goods, template, seller), nil
}

func editableSpotGoods(
	ctx context.Context,
	callerID, goodsID int64,
	updatedAt *timestamppb.Timestamp,
) (*model.SpotGoods, error) {
	if callerID <= 0 || goodsID <= 0 || updatedAt == nil || !updatedAt.IsValid() {
		return nil, connect.NewError(connect.CodeInvalidArgument, errmsg.InvalidArgument)
	}
	goods, err := repository.GetSpotGoodsByID(ctx, goodsID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errmsg.SpotGoodsNotFound)
	}
	if err != nil {
		return nil, spotInternalError()
	}
	if goods.SellerID != callerID {
		return nil, connect.NewError(connect.CodePermissionDenied, errmsg.SpotPermissionDenied)
	}
	if goods.ClosedAt != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errmsg.SpotGoodsClosed)
	}
	if !goods.UpdatedAt.Equal(updatedAt.AsTime()) {
		return nil, connect.NewError(connect.CodeAborted, errmsg.SpotGoodsVersionConflict)
	}
	return goods, nil
}

func UpdateSpotGoodsStock(
	ctx context.Context,
	callerID int64,
	goodsID int64,
	newStockTotal int32,
	updatedAt *timestamppb.Timestamp,
) error {
	if newStockTotal < 0 {
		return connect.NewError(connect.CodeInvalidArgument, errmsg.InvalidArgument)
	}
	goods, err := editableSpotGoods(ctx, callerID, goodsID, updatedAt)
	if err != nil {
		return err
	}
	err = postgres.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		rows, err := repository.UpdateSpotGoodsStockTx(ctx, tx, goodsID, newStockTotal, goods.UpdatedAt)
		if err != nil {
			return spotInternalError()
		}
		if rows == 0 {
			return connect.NewError(connect.CodeAborted, errmsg.SpotGoodsVersionConflict)
		}
		if err := repository.CreateStockLedger(ctx, tx, &model.SpotStockLedger{
			ListingID:  goodsID,
			Delta:      newStockTotal - goods.StockTotal,
			Reason:     model.StockLedgerReasonManualAdjust,
			OperatorID: &callerID,
		}); err != nil {
			return spotInternalError()
		}
		return nil
	})
	return err
}

func CloseSpotGoods(
	ctx context.Context,
	callerID int64,
	goodsID int64,
	updatedAt *timestamppb.Timestamp,
) error {
	if callerID <= 0 || goodsID <= 0 || updatedAt == nil || !updatedAt.IsValid() {
		return connect.NewError(connect.CodeInvalidArgument, errmsg.SpotGoodsVersionConflict)
	}

	goods, err := repository.GetSpotGoodsByID(ctx, goodsID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return connect.NewError(connect.CodeNotFound, errmsg.SpotGoodsNotFound)
		}
		log.Error().Err(err).Int64("goods_id", goodsID).Msg("failed to get spot good before closing")
		return spotInternalError()
	}
	if goods.SellerID != callerID {
		return connect.NewError(connect.CodePermissionDenied, errmsg.SpotPermissionDenied)
	}
	if goods.ClosedAt != nil {
		return connect.NewError(connect.CodeFailedPrecondition, errmsg.SpotGoodsClosed)
	}
	if !timeutil.SameUpdatedAtSecond(goods.UpdatedAt, updatedAt.AsTime()) {
		return connect.NewError(connect.CodeAborted, errmsg.SpotGoodsVersionConflict)
	}

	rows, err := repository.CloseSpotGoods(ctx, goodsID, updatedAt.AsTime())
	if err != nil {
		log.Error().Err(err).Int64("goods_id", goodsID).Msg("failed to close spot good")
		return spotInternalError()
	}
	if rows == 0 {
		return connect.NewError(connect.CodeAborted, errmsg.SpotGoodsVersionConflict)
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
	if newSalePriceCents <= 0 {
		return connect.NewError(connect.CodeInvalidArgument, errmsg.InvalidArgument)
	}
	goods, err := editableSpotGoods(ctx, callerID, goodsID, updatedAt)
	if err != nil {
		return err
	}
	rows, err := repository.UpdateSpotGoodsPrice(ctx, goodsID, newSalePriceCents, goods.UpdatedAt)
	if err != nil {
		return spotInternalError()
	}
	if rows == 0 {
		return connect.NewError(connect.CodeAborted, errmsg.SpotGoodsVersionConflict)
	}
	return nil
}

func getProductTemplates(
	ctx context.Context,
	goodsList []*model.SpotGoods,
) (map[int64]*catalogv1.ProductTemplate, error) {
	templates := make(map[int64]*catalogv1.ProductTemplate)
	if len(goodsList) == 0 {
		return templates, nil
	}

	productTemplateIDs := make([]int64, 0, len(goodsList))
	seen := make(map[int64]struct{}, len(goodsList))
	for _, goods := range goodsList {
		if _, ok := seen[goods.ProductTemplateID]; ok {
			continue
		}
		seen[goods.ProductTemplateID] = struct{}{}
		productTemplateIDs = append(productTemplateIDs, goods.ProductTemplateID)
	}

	resp, err := client.CatalogInternalServiceClient.GetProductTemplates(
		ctx,
		connect.NewRequest(&catalogv1.GetProductTemplatesRequest{
			ProductTemplateIds: productTemplateIDs,
		}),
	)
	if err != nil {
		log.Error().
			Err(err).
			Interface("product_template_ids", productTemplateIDs).
			Msg("failed to get product templates for spot goods")
		return nil, err
	}
	for _, template := range resp.Msg.ProductTemplates {
		templates[template.Id] = template
	}
	return templates, nil
}

func modelToBrief(
	goods *model.SpotGoods,
	template *catalogv1.ProductTemplate,
) *spotv1.SpotGoodsBrief {
	return &spotv1.SpotGoodsBrief{
		Id:              goods.ID,
		ProductTemplate: templateForListing(template, goods.StoreID),
		SalePriceCents:  goods.SalePriceCents,
		CreatedAt:       timestamppb.New(goods.CreatedAt),
		UpdatedAt:       timestamppb.New(goods.UpdatedAt),
	}
}

func modelToDetail(
	goods *model.SpotGoods,
	template *catalogv1.ProductTemplate,
	seller *userv1.UserInfo,
) *spotv1.SpotGoodsDetail {
	return &spotv1.SpotGoodsDetail{
		Id:              goods.ID,
		ProductTemplate: templateForListing(template, goods.StoreID),
		SalePriceCents:  goods.SalePriceCents,
		CreatedAt:       timestamppb.New(goods.CreatedAt),
		UpdatedAt:       timestamppb.New(goods.UpdatedAt),
		Stock:           goods.StockTotal,
		Seller:          seller,
	}
}

func templateForListing(template *catalogv1.ProductTemplate, storeID int64) *catalogv1.ProductTemplate {
	if template == nil || template.StoreId == storeID {
		return template
	}
	// Existing listings retain their original store after a template is moved.
	snapshot := proto.CloneOf(template)
	snapshot.StoreId = storeID
	return snapshot
}
