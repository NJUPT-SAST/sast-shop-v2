package v1

import (
	"context"
	"errors"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/errand/v1/errandv1connect"
	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	rpcinterceptor "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/service"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ErrandDemandServiceServer struct {
	errandv1connect.ErrandDemandServiceHandler
}

// CreateErrandDemand 买家创建跑腿需求订单。
func (s *ErrandDemandServiceServer) CreateErrandDemand(
	ctx context.Context,
	r *connect.Request[errandv1.CreateErrandDemandRequest],
) (*connect.Response[errandv1.CreateErrandDemandResponse], error) {
	msg := r.Msg

	// 1. 从 session 获取用户 ID
	requesterID := getUserIDFromContext(ctx)
	if requesterID == 0 {
		log.Warn().Msg("CreateErrandDemand: user not authenticated")
		return nil, errandError()
	}

	// 2. 参数基本校验（protovalidate 已做格式校验）
	if msg.StoreId <= 0 {
		log.Warn().Int64("store_id", msg.StoreId).Msg("invalid store_id")
		return nil, errandError()
	}
	if len(msg.DemandItems) == 0 {
		log.Warn().Msg("demand_items is empty")
		return nil, errandError()
	}

	// 3. 转换 proto 到 service 层数据结构
	deadline := msg.Deadline.AsTime()
	items := make([]service.DemandItemDraft, 0, len(msg.DemandItems))
	for _, item := range msg.DemandItems {
		items = append(items, service.DemandItemDraft{
			ProductTemplateID:      item.ProductTemplateId,
			Quantity:               item.Quantity,
			ServiceFeePerUnitCents: item.ServiceFeePerUnitCents,
			UpdatedAt:              item.UpdatedAt.AsTime(),
		})
	}

	// 4. 调用 service 层
	demandID, err := service.CreateErrandDemand(ctx, requesterID, msg.StoreId, deadline, items)
	if err != nil {
		// service 层已经记过日志了，这里只翻译成 connect.Error
		if errors.Is(err, service.ErrProductInvalid) {
			log.Warn().Err(err).Msg("product validation failed")
		} else if errors.Is(err, service.ErrDuplicateProduct) {
			log.Warn().Err(err).Msg("duplicate product in demand items")
		} else if errors.Is(err, service.ErrInvalidDeadline) {
			log.Warn().Err(err).Msg("invalid deadline")
		}
		return nil, errandError()
	}

	// 5. 返回响应
	return connect.NewResponse(&errandv1.CreateErrandDemandResponse{
		ErrandDemandId: demandID,
	}), nil
}

// GetDemandList 团长获取未接单跑腿需求大厅列表。
func (s *ErrandDemandServiceServer) GetDemandList(
	ctx context.Context,
	r *connect.Request[errandv1.GetDemandListRequest],
) (*connect.Response[errandv1.GetDemandListResponse], error) {
	msg := r.Msg

	// 1. 从 session 获取用户 ID（用于鉴权，虽然这个接口不需要知道具体是谁）
	userID := getUserIDFromContext(ctx)
	if userID == 0 {
		log.Warn().Msg("GetDemandList: user not authenticated")
		return nil, errandError()
	}

	// 2. 参数处理（service 层会做默认值处理）
	page := msg.Page
	pageSize := msg.PageSize
	storeName := ""
	if msg.StoreName != nil {
		storeName = *msg.StoreName
	}

	// 3. 调用 service 层
	results, totalCount, err := service.GetDemandList(ctx, page, pageSize, storeName)
	if err != nil {
		log.Error().Err(err).Msg("GetDemandList failed")
		return nil, errandError()
	}

	// 4. 组装响应
	demands := make([]*errandv1.ErrandDemandByStore, 0, len(results))
	for _, result := range results {
		demands = append(demands, &errandv1.ErrandDemandByStore{
			StoreId:                   result.StoreID,
			StoreName:                 result.StoreName,
			ParticipantAvatars:        result.ParticipantAvatars,
			TotalOriginUnitPriceCents: result.TotalOriginUnitPriceCents,
			TotalServiceFeeCents:      result.TotalServiceFeeCents,
			UpdatedAt:                 timestamppb.New(result.UpdatedAt),
		})
	}

	return connect.NewResponse(&errandv1.GetDemandListResponse{
		Demands:     demands,
		CurrentPage: page,
		TotalCount:  totalCount,
	}), nil
}

// GetDemandDetail 团长查看某个店铺下可接单的需求详情。
func (s *ErrandDemandServiceServer) GetDemandDetail(
	ctx context.Context,
	r *connect.Request[errandv1.GetDemandDetailRequest],
) (*connect.Response[errandv1.GetDemandDetailResponse], error) {
	msg := r.Msg

	// 1. 从 session 获取用户 ID
	userID := getUserIDFromContext(ctx)
	if userID == 0 {
		log.Warn().Msg("GetDemandDetail: user not authenticated")
		return nil, errandError()
	}

	// 2. 参数校验
	if msg.StoreId <= 0 {
		log.Warn().Int64("store_id", msg.StoreId).Msg("invalid store_id")
		return nil, errandError()
	}

	// 3. 调用 service 层
	results, err := service.GetDemandDetail(ctx, msg.StoreId)
	if err != nil {
		log.Error().Err(err).Int64("store_id", msg.StoreId).Msg("GetDemandDetail failed")
		return nil, errandError()
	}

	// 4. 组装响应
	details := make([]*errandv1.ErrandDemandDetail, 0, len(results))
	for _, result := range results {
		requesters := make([]*errandv1.ErrandDemandDetailRequester, 0, len(result.Requesters))
		for _, req := range result.Requesters {
			requesters = append(requesters, &errandv1.ErrandDemandDetailRequester{
				RequesterId:            req.RequesterID,
				RequesterName:          req.RequesterName,
				RequesterAvatarUrl:     req.RequesterAvatarURL,
				Quantity:               req.Quantity,
				ServiceFeePerUnitCents: req.ServiceFeePerUnitCents,
				ErrandDemandItemId:     req.ErrandDemandItemID,
				Deadline:               timestamppb.New(req.Deadline),
				UpdatedAt:              timestamppb.New(req.UpdatedAt),
			})
		}

		details = append(details, &errandv1.ErrandDemandDetail{
			ErrandDemandId: result.ErrandDemandID,
			ProductTemplate: &catalogv1.ProductTemplate{
				Id:           result.ProductTemplateID,
				Title:        result.ProductTitle,
				Description:  result.ProductDescription,
				MainImageUrl: result.ProductImageURL,
				// 其他字段根据需要填充
			},
			EstimatedUnitPriceCents: result.EstimatedUnitPriceCents,
			Quantity:                result.TotalQuantity,
			Requesters:              requesters,
		})
	}

	return connect.NewResponse(&errandv1.GetDemandDetailResponse{
		Details: details,
	}), nil
}

// getUserIDFromContext 从 context 中提取用户 ID
func getUserIDFromContext(ctx context.Context) int64 {
	user, ok := rpcinterceptor.UserFromContext(ctx)
	if !ok {
		return 0
	}
	return user.UserID
}
func InitErrandDemandServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := errandv1connect.NewErrandDemandServiceHandler(&ErrandDemandServiceServer{}, opts...)
	log.Debug().Msgf("ErrandDemandService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
