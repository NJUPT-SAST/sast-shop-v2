package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/spot/v1/spotv1connect"
	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	"connectrpc.com/connect"
	rpcinterceptor "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/service"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type SpotGoodsServiceServer struct {
	spotv1connect.SpotGoodsServiceHandler
}

func (s *SpotGoodsServiceServer) ListSpotGoods(
	ctx context.Context,
	r *connect.Request[spotv1.ListSpotGoodsRequest],
) (*connect.Response[spotv1.ListSpotGoodsResponse], error) {
	if r.Msg.Page < 1 || r.Msg.PageSize <= 0 {
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR},
		}, "invalid pagination parameters")
	}
	offset := int((r.Msg.Page - 1) * r.Msg.PageSize)
	limit := int(r.Msg.PageSize)

	spotGoodsBrief, err := service.ListSpotGoods(
		ctx,
		r.Msg.StoreId,
		offset,
		limit,
	)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to list spot goods for storeID: %d", r.Msg.StoreId)
		return nil, err
	}
	totalCount, err := service.GetSpotGoodLength(ctx, r.Msg.StoreId)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get spot goods length for storeID: %d", r.Msg.StoreId)
		return nil, err
	}
	return connect.NewResponse(&spotv1.ListSpotGoodsResponse{
		SpotGoodsList: spotGoodsBrief,
		CurrentPage:   r.Msg.Page,
		TotalCount:    totalCount,
	}), nil
}

func (s *SpotGoodsServiceServer) GetSpotGoods(
	ctx context.Context,
	r *connect.Request[spotv1.GetSpotGoodsRequest],
) (*connect.Response[spotv1.GetSpotGoodsResponse], error) {
	detail, err := service.GetSpotGoods(ctx, r.Msg.SpotGoodsId)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get spot good info for goodsID: %d", r.Msg.SpotGoodsId)
		return nil, err
	}
	return connect.NewResponse(&spotv1.GetSpotGoodsResponse{
		SpotGoodsDetail: detail,
	}), nil
}

func (s *SpotGoodsServiceServer) CreateSpotGoods(
	ctx context.Context,
	r *connect.Request[spotv1.CreateSpotGoodsRequest],
) (*connect.Response[spotv1.CreateSpotGoodsResponse], error) {
	user, ok := rpcinterceptor.UserFromContext(ctx)
	if !ok {
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "user not found in context")
	}
	goods := &model.SpotGoods{
		SellerID:          user.UserID,
		StoreID:           r.Msg.StoreId,
		ProductTemplateID: r.Msg.ProductTemplateId,
		SalePriceCents:    r.Msg.SalePriceCents,
		StockTotal:        r.Msg.StockTotal,
	}
	detail, err := service.CreateSpotGoods(ctx, goods)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to create spot good: %v", goods)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	return connect.NewResponse(&spotv1.CreateSpotGoodsResponse{
		SpotGoodsDetail: detail,
	}), nil
}

func (s *SpotGoodsServiceServer) UpdateSpotGoodsStock(
	ctx context.Context,
	r *connect.Request[spotv1.UpdateSpotGoodsStockRequest],
) (*connect.Response[spotv1.UpdateSpotGoodsStockResponse], error) {
	user, ok := rpcinterceptor.UserFromContext(ctx)
	if !ok {
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "user not found in context")
	}
	err := service.UpdateSpotGoodsStock(ctx, user.UserID, r.Msg.SpotGoodsId, r.Msg.NewStock, r.Msg.UpdatedAt)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to update spot good stock total for goodsID: %d", r.Msg.SpotGoodsId)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	return connect.NewResponse(&spotv1.UpdateSpotGoodsStockResponse{}), nil
}

func (s *SpotGoodsServiceServer) UpdateSpotGoodsPrice(
	ctx context.Context,
	r *connect.Request[spotv1.UpdateSpotGoodsPriceRequest],
) (*connect.Response[spotv1.UpdateSpotGoodsPriceResponse], error) {
	user, ok := rpcinterceptor.UserFromContext(ctx)
	if !ok {
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "user not found in context")
	}
	err := service.UpdateSpotGoodsPrice(ctx, user.UserID, r.Msg.SpotGoodsId, r.Msg.NewSalePriceCents, r.Msg.UpdatedAt)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to update spot good sale price for goodsID: %d", r.Msg.SpotGoodsId)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	return connect.NewResponse(&spotv1.UpdateSpotGoodsPriceResponse{}), nil
}

func InitSpotGoodsServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := spotv1connect.NewSpotGoodsServiceHandler(&SpotGoodsServiceServer{}, opts...)
	log.Debug().Msgf("SpotGoodsService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
