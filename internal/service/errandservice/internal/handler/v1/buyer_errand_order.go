package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/errand/v1/errandv1connect"
	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/service"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type BuyerErrandOrderServiceServer struct {
	errandv1connect.BuyerErrandOrderServiceHandler
}

func (s *BuyerErrandOrderServiceServer) GetBuyerErrandOrderBrief(
	ctx context.Context,
	r *connect.Request[errandv1.GetBuyerErrandOrderBriefRequest],
) (*connect.Response[errandv1.GetBuyerErrandOrderBriefResponse], error) {
	msg := r.Msg

	requesterID := getUserIDFromContext(ctx)
	if requesterID == 0 {
		log.Warn().Msg("GetBuyerErrandOrderBrief: user not authenticated")
		return nil, errandError()
	}

	var storeID *int64
	if msg.StoreIdFilter != nil {
		storeID = msg.StoreIdFilter
	}

	var status *model.ErrandDemandStatus
	if msg.StatusFilter != nil {
		s := demandStatusFromProto(*msg.StatusFilter)
		status = &s
	}

	results, totalCount, err := service.GetBuyerOrderBriefList(ctx, requesterID, storeID, status, msg.Page, msg.PageSize)
	if err != nil {
		log.Error().Err(err).Msg("GetBuyerOrderBriefList failed")
		return nil, errandError()
	}

	orders := make([]*errandv1.BuyerErrandOrderBrief, 0, len(results))
	for _, r := range results {
		protoTemplates := append([]*catalogv1.ProductTemplate{}, r.ProductTemplates...)

		orders = append(orders, &errandv1.BuyerErrandOrderBrief{
			ErrandDemandId:         r.ErrandDemandID,
			StoreId:                r.StoreID,
			CreatedAt:              timestamppb.New(r.CreatedAt),
			StoreInfo:              r.StoreInfo,
			Status:                 demandStatusToProto(r.Status),
			ProductTemplates:       protoTemplates,
			TotalOriginAmountCents: r.TotalOriginAmountCents,
			TotalActualAmountCents: r.TotalActualAmountCents,
			TotalServiceFeeCents:   r.TotalServiceFeeCents,
			ProductTotalCount:      r.ProductTotalCount,
		})
	}

	totalCount32 := int32(totalCount) //nolint:gosec
	return connect.NewResponse(&errandv1.GetBuyerErrandOrderBriefResponse{
		Orders:      orders,
		CurrentPage: msg.Page,
		TotalCount:  totalCount32,
	}), nil
}

func (s *BuyerErrandOrderServiceServer) GetBuyerErrandOrderDetail(
	ctx context.Context,
	r *connect.Request[errandv1.GetBuyerErrandOrderDetailRequest],
) (*connect.Response[errandv1.GetBuyerErrandOrderDetailResponse], error) {
	msg := r.Msg

	requesterID := getUserIDFromContext(ctx)
	if requesterID == 0 {
		log.Warn().Msg("GetBuyerErrandOrderDetail: user not authenticated")
		return nil, errandError()
	}

	if msg.ErrandDemandId <= 0 {
		log.Warn().Int64("errand_demand_id", msg.ErrandDemandId).Msg("invalid errand_demand_id")
		return nil, errandError()
	}

	detail, err := service.GetBuyerOrderDetail(ctx, requesterID, msg.ErrandDemandId)
	if err != nil {
		log.Error().Err(err).Int64("demand_id", msg.ErrandDemandId).Msg("GetBuyerOrderDetail failed")
		return nil, errandError()
	}

	productItems := make([]*errandv1.BuyerErrandOrderProductItem, 0, len(detail.ProductItems))
	for _, pi := range detail.ProductItems {
		actualPrice := int32(0)
		if pi.ActualUnitPriceCents != nil {
			actualPrice = *pi.ActualUnitPriceCents
		}

		productItems = append(productItems, &errandv1.BuyerErrandOrderProductItem{
			ProductTemplate:        pi.ProductTemplate,
			ActualUnitPriceCents:   actualPrice,
			RequiredQuantity:       pi.RequiredQuantity,
			PurchasedQuantity:      pi.PurchasedQuantity,
			NonPurchaseReason:      &pi.NonPurchaseReason,
			DistributedQuantity:    &pi.DistributedQuantity,
			ServiceFeePerUnitCents: pi.ServiceFeePerUnitCents,
			SubtotalCents:          0,
			ErrandDemandItemId:     pi.ErrandDemandItemID,
		})
	}

	protoDetail := &errandv1.BuyerErrandOrderDetail{
		ErrandDemandId:         detail.ErrandDemandID,
		StoreId:                detail.StoreID,
		CreatedAt:              timestamppb.New(detail.CreatedAt),
		StoreInfo:              detail.StoreInfo,
		Status:                 demandStatusToProto(detail.Status),
		ProductItems:           productItems,
		TotalOriginAmountCents: detail.TotalOriginAmountCents,
		TotalActualAmountCents: detail.TotalActualAmountCents,
		TotalServiceFeeCents:   detail.TotalServiceFeeCents,
		CaptainInfo:            detail.CaptainInfo,
		Deadline:               timestamppb.New(detail.Deadline),
	}

	if detail.ShoppingStartAt != nil {
		protoDetail.ShoppingStartAt = timestamppb.New(*detail.ShoppingStartAt)
	}
	if detail.ShoppingCompletedAt != nil {
		protoDetail.ShoppingCompletedAt = timestamppb.New(*detail.ShoppingCompletedAt)
	}
	if detail.DistributionCompletedAt != nil {
		protoDetail.DistributionCompletedAt = timestamppb.New(*detail.DistributionCompletedAt)
	}
	if detail.PaymentCompletedAt != nil {
		protoDetail.PaymentCompletedAt = timestamppb.New(*detail.PaymentCompletedAt)
	}
	if detail.CancelledAt != nil {
		protoDetail.CancelledAt = timestamppb.New(*detail.CancelledAt)
	}

	return connect.NewResponse(&errandv1.GetBuyerErrandOrderDetailResponse{
		Order: protoDetail,
	}), nil
}

func demandStatusFromProto(s errandv1.ErrandDemandStatus) model.ErrandDemandStatus {
	switch s {
	case errandv1.ErrandDemandStatus_ERRAND_DEMAND_STATUS_OPEN:
		return model.ErrandDemandStatusOpen
	case errandv1.ErrandDemandStatus_ERRAND_DEMAND_STATUS_SHOPPING:
		return model.ErrandDemandStatusShopping
	case errandv1.ErrandDemandStatus_ERRAND_DEMAND_STATUS_PENDING_DISTRIBUTING:
		return model.ErrandDemandStatusPendingDistributing
	case errandv1.ErrandDemandStatus_ERRAND_DEMAND_STATUS_DISTRIBUTING:
		return model.ErrandDemandStatusDistributing
	case errandv1.ErrandDemandStatus_ERRAND_DEMAND_STATUS_PENDING_PAYMENT:
		return model.ErrandDemandStatusPendingPayment
	case errandv1.ErrandDemandStatus_ERRAND_DEMAND_STATUS_COMPLETED:
		return model.ErrandDemandStatusCompleted
	case errandv1.ErrandDemandStatus_ERRAND_DEMAND_STATUS_CANCELLED:
		return model.ErrandDemandStatusCancelled
	default:
		return model.ErrandDemandStatusOpen
	}
}

func demandStatusToProto(s model.ErrandDemandStatus) errandv1.ErrandDemandStatus {
	switch s {
	case model.ErrandDemandStatusOpen:
		return errandv1.ErrandDemandStatus_ERRAND_DEMAND_STATUS_OPEN
	case model.ErrandDemandStatusShopping:
		return errandv1.ErrandDemandStatus_ERRAND_DEMAND_STATUS_SHOPPING
	case model.ErrandDemandStatusPendingDistributing:
		return errandv1.ErrandDemandStatus_ERRAND_DEMAND_STATUS_PENDING_DISTRIBUTING
	case model.ErrandDemandStatusDistributing:
		return errandv1.ErrandDemandStatus_ERRAND_DEMAND_STATUS_DISTRIBUTING
	case model.ErrandDemandStatusPendingPayment:
		return errandv1.ErrandDemandStatus_ERRAND_DEMAND_STATUS_PENDING_PAYMENT
	case model.ErrandDemandStatusCompleted:
		return errandv1.ErrandDemandStatus_ERRAND_DEMAND_STATUS_COMPLETED
	case model.ErrandDemandStatusCancelled:
		return errandv1.ErrandDemandStatus_ERRAND_DEMAND_STATUS_CANCELLED
	default:
		return errandv1.ErrandDemandStatus_ERRAND_DEMAND_STATUS_UNSPECIFIED
	}
}

func InitBuyerErrandOrderServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := errandv1connect.NewBuyerErrandOrderServiceHandler(&BuyerErrandOrderServiceServer{}, opts...)
	log.Debug().Msgf("BuyerErrandOrderService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
