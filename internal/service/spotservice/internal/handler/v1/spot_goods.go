package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/spot/v1/spotv1connect"
	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/service"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type SpotGoodsServiceServer struct {
	spotv1connect.SpotGoodsServiceHandler
}

func (s *SpotGoodsServiceServer) ListSpotGoods(
	ctx context.Context,
	r *connect.Request[spotv1.ListSpotGoodsRequest],
) (*connect.Response[spotv1.ListSpotGoodsResponse], error) {
	spotGoodsList, err := service.ListSpotGoods(
		ctx,
		r.Msg.StoreId,
		int((r.Msg.Page-1)*r.Msg.PageSize),
		int(r.Msg.PageSize),
	)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to list spot goods for storeID: %d", r.Msg.StoreId)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	var spotGoodsBrief []*spotv1.SpotGoodsBrief
	for _, goods := range spotGoodsList {
		spotGoodsBrief = append(spotGoodsBrief, &spotv1.SpotGoodsBrief{
			Id:             goods.ID,
			SalePriceCents: goods.SalePriceCents,
			CreatedAt:      timestamppb.New(goods.CreatedAt),
			UpdatedAt:      timestamppb.New(goods.UpdatedAt),
		})
	}
	totalCount, err := service.GetSpotGoodLength(ctx, r.Msg.StoreId)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get spot goods length for storeID: %d", r.Msg.StoreId)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	return connect.NewResponse(&spotv1.ListSpotGoodsResponse{
		SpotGoodsList: spotGoodsBrief,
		TotalCount:    totalCount,
	}), nil
}

func (s *SpotGoodsServiceServer) GetSpotGoods(
	ctx context.Context,
	r *connect.Request[spotv1.GetSpotGoodsRequest],
) (*connect.Response[spotv1.GetSpotGoodsResponse], error) {
	goods, err := service.GetSpotGoods(ctx, r.Msg.SpotGoodsId)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get spot good info for goodsID: %d", r.Msg.SpotGoodsId)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	return connect.NewResponse(&spotv1.GetSpotGoodsResponse{
		SpotGoodsDetail: &spotv1.SpotGoodsDetail{
			Id:             goods.ID,
			SalePriceCents: goods.SalePriceCents,
			CreatedAt:      timestamppb.New(goods.CreatedAt),
			UpdatedAt:      timestamppb.New(goods.UpdatedAt),
			Stock:          goods.StockTotal,
		},
	}), nil
}

func (s *SpotGoodsServiceServer) CreateSpotGoods(
	ctx context.Context,
	r *connect.Request[spotv1.CreateSpotGoodsRequest],
) (*connect.Response[spotv1.CreateSpotGoodsResponse], error) {
	goods := &model.SpotGoods{
		SalePriceCents: r.Msg.SalePriceCents,
		StockTotal:     r.Msg.StockTotal,
	}
	err := service.CreateSpotGoods(ctx, goods)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to create spot good: %v", goods)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
			SpotError: &spotv1.SpotError{
				Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	return connect.NewResponse(&spotv1.CreateSpotGoodsResponse{
		SpotGoodsDetail: &spotv1.SpotGoodsDetail{
			Id:             goods.ID,
			SalePriceCents: goods.SalePriceCents,
			CreatedAt:      timestamppb.New(goods.CreatedAt),
			UpdatedAt:      timestamppb.New(goods.UpdatedAt),
			Stock:          goods.StockTotal,
		},
	}), nil
}

func (s *SpotGoodsServiceServer) UpdateSpotGoodsStock(
	ctx context.Context,
	r *connect.Request[spotv1.UpdateSpotGoodsStockRequest],
) (*connect.Response[spotv1.UpdateSpotGoodsStockResponse], error) {
	err := service.UpdateSpotGoodsStock(ctx, r.Msg.SpotGoodsId, r.Msg.NewStock)
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
	err := service.UpdateSpotGoodsPrice(ctx, r.Msg.SpotGoodsId, r.Msg.NewSalePriceCents)
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
