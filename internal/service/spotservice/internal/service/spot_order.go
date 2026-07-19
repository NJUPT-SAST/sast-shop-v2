package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/idgen"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/client"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/repository"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrInvalidCreateSpotOrdersRequest = errors.New("invalid create spot orders request")
	ErrSpotGoodsNotFound              = errors.New("spot goods not found")
	ErrSpotGoodsClosed                = errors.New("spot goods closed")
	ErrSpotGoodsVersionConflict       = errors.New("spot goods updated_at version conflict")
	ErrInsufficientStock              = errors.New("insufficient stock")
	ErrSpotOrderNotFound              = errors.New("spot order not found")
	ErrSpotOrderPermissionDenied      = errors.New("permission denied for spot order")
	ErrSpotOrderVersionConflict       = errors.New("spot order updated_at version conflict")
	ErrInvalidSpotOrderStatus         = errors.New("invalid spot order status")
)

const (
	defaultPageSize   = 20
	maxPageSize       = 100
	spotOrderNoPrefix = "SO"
)

func ListSpotOrder(
	ctx context.Context,
	userID int64,
	req *spotv1.ListSpotOrderRequest,
) (*spotv1.ListSpotOrderResponse, error) {
	if userID <= 0 || req == nil || req.StoreId <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidCreateSpotOrdersRequest)
	}

	perspective, err := parsePerspective(req.Perspective)
	if err != nil {
		return nil, err
	}

	var statusFilter *model.SpotOrderStatus
	if req.GetFilterStatus() != spotv1.SpotOrderStatus_SPOT_ORDER_STATUS_UNSPECIFIED {
		status, ok := protoStatusToModel(req.GetFilterStatus())
		if !ok {
			return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidSpotOrderStatus)
		}
		statusFilter = &status
	}

	page, pageSize := normalizePage(req.Page, req.PageSize)
	records, total, err := repository.ListSpotOrders(
		ctx,
		userID,
		req.StoreId,
		perspective,
		statusFilter,
		int(pageSize),
		int((page-1)*pageSize),
	)
	if err != nil {
		log.Error().Err(err).Msg("failed to list spot orders")
		return nil, spotInternalError()
	}
	totalCount, err := checkedInt32(total, "total_count")
	if err != nil {
		return nil, connect.NewError(connect.CodeOutOfRange, err)
	}

	storeCache := make(map[int64]*catalogv1.Store)
	briefs := make([]*spotv1.SpotOrderBrief, 0, len(records))
	for _, record := range records {
		store, err := getCachedStore(ctx, storeCache, record.StoreID)
		if err != nil {
			return nil, err
		}
		briefs = append(briefs, spotOrderRecordToBrief(record, store))
	}

	return &spotv1.ListSpotOrderResponse{
		SpotOrders:  briefs,
		CurrentPage: page,
		TotalCount:  totalCount,
	}, nil
}

func GetSpotOrderDetail(
	ctx context.Context,
	userID int64,
	req *spotv1.GetSpotOrderDetailRequest,
) (*spotv1.SpotOrderDetail, error) {
	if userID <= 0 || req == nil || req.SpotOrderId <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidCreateSpotOrdersRequest)
	}

	record, err := repository.GetSpotOrderRecord(ctx, req.SpotOrderId)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, ErrSpotOrderNotFound)
	}
	if err != nil {
		log.Error().
			Err(err).
			Int64("spot_order_id", req.SpotOrderId).
			Msg("failed to get spot order detail")
		return nil, spotInternalError()
	}
	if !canReadSpotOrder(userID, record) {
		return nil, connect.NewError(connect.CodePermissionDenied, ErrSpotOrderPermissionDenied)
	}
	return buildSpotOrderDetail(ctx, record)
}

func CancelSpotOrder(
	ctx context.Context,
	userID int64,
	req *spotv1.CancelSpotOrderRequest,
) (*spotv1.SpotOrderDetail, error) {
	if invalidCancelSpotOrderRequest(userID, req) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidCreateSpotOrdersRequest)
	}

	err := repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return cancelSpotOrderInTx(ctx, tx, userID, req)
	})
	if err != nil {
		return nil, err
	}

	return GetSpotOrderDetail(
		ctx,
		userID,
		&spotv1.GetSpotOrderDetailRequest{SpotOrderId: req.SpotOrderId},
	)
}

func invalidCancelSpotOrderRequest(userID int64, req *spotv1.CancelSpotOrderRequest) bool {
	return userID <= 0 ||
		req == nil ||
		req.SpotOrderId <= 0 ||
		req.UpdatedAt == nil ||
		!req.UpdatedAt.IsValid()
}

func cancelSpotOrderInTx(
	ctx context.Context,
	tx bun.Tx,
	userID int64,
	req *spotv1.CancelSpotOrderRequest,
) error {
	order, _, err := lockOrderAndGoods(ctx, tx, req.SpotOrderId)
	if err != nil {
		return err
	}
	if err := validateCancelableSpotOrder(order, userID, req.UpdatedAt.AsTime()); err != nil {
		return err
	}
	if err := releaseSpotOrderStock(ctx, tx, order, userID); err != nil {
		return err
	}
	if err := cancelSpotOrderBill(ctx, order); err != nil {
		return err
	}
	return markSpotOrderCancelled(ctx, tx, order)
}

func validateCancelableSpotOrder(
	order *model.SpotOrder,
	userID int64,
	expectedUpdatedAt time.Time,
) error {
	if order.PurchaserID != userID {
		return connect.NewError(connect.CodePermissionDenied, ErrSpotOrderPermissionDenied)
	}
	if !order.UpdatedAt.Equal(expectedUpdatedAt) {
		return connect.NewError(connect.CodeAborted, ErrSpotOrderVersionConflict)
	}
	if order.Status != model.SpotOrderStatusPendingPayment {
		return connect.NewError(connect.CodeFailedPrecondition, ErrInvalidSpotOrderStatus)
	}
	return nil
}

func releaseSpotOrderStock(
	ctx context.Context,
	tx bun.Tx,
	order *model.SpotOrder,
	operatorID int64,
) error {
	if err := repository.IncreaseSpotGoodsStock(ctx, tx, order.ListingID, order.Quantity); err != nil {
		log.Error().
			Err(err).
			Int64("spot_order_id", order.ID).
			Msg("failed to release spot goods stock")
		return spotInternalError()
	}
	if err := insertCancelStockLedger(ctx, tx, order, operatorID); err != nil {
		log.Error().
			Err(err).
			Int64("spot_order_id", order.ID).
			Msg("failed to insert cancel stock ledger")
		return spotInternalError()
	}
	return nil
}

func insertCancelStockLedger(
	ctx context.Context,
	tx bun.Tx,
	order *model.SpotOrder,
	operatorID int64,
) error {
	refID := order.ID
	return repository.InsertStockLedger(ctx, tx, &model.SpotStockLedger{
		ListingID:  order.ListingID,
		Delta:      order.Quantity,
		Reason:     model.StockLedgerReasonOrderCancel,
		RefType:    "spot_order",
		RefID:      &refID,
		OperatorID: &operatorID,
	})
}

func cancelSpotOrderBill(ctx context.Context, order *model.SpotOrder) error {
	if order.PaymentBillID == nil {
		return nil
	}
	_, err := client.PaymentInternalServiceClient.CancelBillBySource(
		ctx,
		connect.NewRequest(&paymentv1.CancelBillBySourceRequest{
			SourceType: "spot_order",
			SourceId:   order.ID,
			PayerId:    &order.PurchaserID,
		}),
	)
	if err != nil {
		log.Error().
			Err(err).
			Int64("spot_order_id", order.ID).
			Msg("failed to cancel payment bill")
	}
	return err
}

func markSpotOrderCancelled(ctx context.Context, tx bun.Tx, order *model.SpotOrder) error {
	if _, err := repository.MarkSpotOrderCancelled(ctx, tx, order.ID); err != nil {
		log.Error().
			Err(err).
			Int64("spot_order_id", order.ID).
			Msg("failed to mark spot order cancelled")
		return spotInternalError()
	}
	return nil
}

func CompleteSpotOrder(
	ctx context.Context,
	userID int64,
	req *spotv1.CompleteSpotOrderRequest,
) (*spotv1.SpotOrderDetail, error) {
	if invalidCompleteSpotOrderRequest(userID, req) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidCreateSpotOrdersRequest)
	}

	err := repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		order, goods, err := lockOrderAndGoods(ctx, tx, req.SpotOrderId)
		if err != nil {
			return err
		}
		if goods.SellerID != userID {
			return connect.NewError(connect.CodePermissionDenied, ErrSpotOrderPermissionDenied)
		}
		if !order.UpdatedAt.Equal(req.UpdatedAt.AsTime()) {
			return connect.NewError(connect.CodeAborted, ErrSpotOrderVersionConflict)
		}
		if order.Status != model.SpotOrderStatusPaid {
			return connect.NewError(connect.CodeFailedPrecondition, ErrInvalidSpotOrderStatus)
		}
		if _, err := repository.MarkSpotOrderCompleted(ctx, tx, order.ID); err != nil {
			log.Error().
				Err(err).
				Int64("spot_order_id", order.ID).
				Msg("failed to mark spot order completed")
			return spotInternalError()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return GetSpotOrderDetail(
		ctx,
		userID,
		&spotv1.GetSpotOrderDetailRequest{SpotOrderId: req.SpotOrderId},
	)
}

func invalidCompleteSpotOrderRequest(userID int64, req *spotv1.CompleteSpotOrderRequest) bool {
	return userID <= 0 ||
		req == nil ||
		req.SpotOrderId <= 0 ||
		req.UpdatedAt == nil ||
		!req.UpdatedAt.IsValid()
}

func CreateSpotOrders(
	ctx context.Context,
	purchaserID int64,
	req *spotv1.CreateSpotOrdersRequest,
) ([]*spotv1.SpotOrderDetail, error) {
	if purchaserID <= 0 || req == nil || len(req.SpotOrders) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidCreateSpotOrdersRequest)
	}

	details := make([]*spotv1.SpotOrderDetail, 0, len(req.SpotOrders))
	err := repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		for _, item := range req.SpotOrders {
			detail, err := createOneSpotOrder(ctx, tx, purchaserID, item)
			if err != nil {
				return err
			}
			details = append(details, detail)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return details, nil
}

func lockOrderAndGoods(
	ctx context.Context,
	tx bun.Tx,
	orderID int64,
) (*model.SpotOrder, *model.SpotGoods, error) {
	order, err := repository.LockSpotOrder(ctx, tx, orderID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil, connect.NewError(connect.CodeNotFound, ErrSpotOrderNotFound)
	}
	if err != nil {
		log.Error().
			Err(err).
			Int64("spot_order_id", orderID).
			Msg("failed to lock spot order")
		return nil, nil, spotInternalError()
	}

	goods, err := repository.LockSpotGoods(ctx, tx, order.ListingID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil, connect.NewError(connect.CodeNotFound, ErrSpotGoodsNotFound)
	}
	if err != nil {
		log.Error().
			Err(err).
			Int64("listing_id", order.ListingID).
			Msg("failed to lock spot goods")
		return nil, nil, spotInternalError()
	}
	return order, goods, nil
}

func createOneSpotOrder(
	ctx context.Context,
	tx bun.Tx,
	purchaserID int64,
	item *spotv1.CreateSpotOrder,
) (*spotv1.SpotOrderDetail, error) {
	goods, err := lockGoodsForCreateSpotOrder(ctx, tx, item)
	if err != nil {
		return nil, err
	}

	product, err := getProductTemplate(ctx, goods.ProductTemplateID)
	if err != nil {
		return nil, err
	}
	totalAmount, err := checkedTotalAmount(goods.SalePriceCents, item.Quantity)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	order, err := createPendingSpotOrder(ctx, tx, purchaserID, goods, product, item, totalAmount)
	if err != nil {
		return nil, err
	}

	bill, err := createPaymentBillForSpotOrder(ctx, purchaserID, goods, order, totalAmount)
	if err != nil {
		return nil, err
	}
	order, err = attachPaymentBillToSpotOrder(ctx, tx, order.ID, bill.Id)
	if err != nil {
		return nil, err
	}

	store, err := getStore(ctx, goods.StoreID)
	if err != nil {
		return nil, err
	}
	return spotOrderToDetail(order, store, product, bill), nil
}

func lockGoodsForCreateSpotOrder(
	ctx context.Context,
	tx bun.Tx,
	item *spotv1.CreateSpotOrder,
) (*model.SpotGoods, error) {
	if err := validateCreateSpotOrderItem(item); err != nil {
		return nil, err
	}

	goods, err := repository.LockSpotGoods(ctx, tx, item.SpotListingId)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, ErrSpotGoodsNotFound)
	}
	if err != nil {
		log.Error().
			Err(err).
			Int64("listing_id", item.SpotListingId).
			Msg("failed to lock spot goods")
		return nil, spotInternalError()
	}
	if err := validateGoodsForCreateSpotOrder(goods, item); err != nil {
		return nil, err
	}
	return goods, nil
}

func validateCreateSpotOrderItem(item *spotv1.CreateSpotOrder) error {
	if item == nil || item.SpotListingId <= 0 || item.Quantity <= 0 {
		return connect.NewError(connect.CodeInvalidArgument, ErrInvalidCreateSpotOrdersRequest)
	}
	if item.UpdatedAt == nil || !item.UpdatedAt.IsValid() {
		return connect.NewError(connect.CodeInvalidArgument, ErrInvalidCreateSpotOrdersRequest)
	}
	return nil
}

func validateGoodsForCreateSpotOrder(goods *model.SpotGoods, item *spotv1.CreateSpotOrder) error {
	if goods.ClosedAt != nil {
		return connect.NewError(connect.CodeFailedPrecondition, ErrSpotGoodsClosed)
	}
	if !goods.UpdatedAt.Equal(item.UpdatedAt.AsTime()) {
		return connect.NewError(connect.CodeAborted, ErrSpotGoodsVersionConflict)
	}
	if goods.StockTotal < item.Quantity {
		return connect.NewError(connect.CodeResourceExhausted, ErrInsufficientStock)
	}
	return nil
}

func createPendingSpotOrder(
	ctx context.Context,
	tx bun.Tx,
	purchaserID int64,
	goods *model.SpotGoods,
	product *catalogv1.ProductTemplate,
	item *spotv1.CreateSpotOrder,
	totalAmount int32,
) (*model.SpotOrder, error) {
	if err := decreaseStockForCreateSpotOrder(ctx, tx, goods.ID, item.Quantity); err != nil {
		return nil, err
	}

	order, err := buildPendingSpotOrder(purchaserID, goods, product, item, totalAmount)
	if err != nil {
		return nil, err
	}
	if err := repository.InsertSpotOrder(ctx, tx, order); err != nil {
		log.Error().Err(err).Msg("failed to insert spot order")
		return nil, spotInternalError()
	}
	if err := insertOrderLockLedger(
		ctx,
		tx,
		goods.ID,
		-item.Quantity,
		order.ID,
		purchaserID,
	); err != nil {
		log.Error().
			Err(err).
			Int64("spot_order_id", order.ID).
			Msg("failed to insert spot stock ledger")
		return nil, spotInternalError()
	}
	return order, nil
}

func decreaseStockForCreateSpotOrder(
	ctx context.Context,
	tx bun.Tx,
	listingID int64,
	quantity int32,
) error {
	if err := repository.DecreaseSpotGoodsStock(ctx, tx, listingID, quantity); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return connect.NewError(connect.CodeResourceExhausted, ErrInsufficientStock)
		}
		log.Error().Err(err).Int64("listing_id", listingID).Msg("failed to decrease spot goods stock")
		return spotInternalError()
	}
	return nil
}

func buildPendingSpotOrder(
	purchaserID int64,
	goods *model.SpotGoods,
	product *catalogv1.ProductTemplate,
	item *spotv1.CreateSpotOrder,
	totalAmount int32,
) (*model.SpotOrder, error) {
	orderNo, err := newSpotOrderNo()
	if err != nil {
		log.Error().Err(err).Msg("failed to generate spot order number")
		return nil, spotInternalError()
	}
	return &model.SpotOrder{
		OrderNo:             orderNo,
		PurchaserID:         purchaserID,
		ListingID:           goods.ID,
		ProductTemplateID:   goods.ProductTemplateID,
		TitleSnapshot:       product.Title,
		DescriptionSnapshot: product.Description,
		ImageURLSnapshot:    product.MainImageUrl,
		Quantity:            item.Quantity,
		UnitPriceCents:      goods.SalePriceCents,
		TotalAmountCents:    totalAmount,
		Status:              model.SpotOrderStatusPendingPayment,
	}, nil
}

func insertOrderLockLedger(
	ctx context.Context,
	tx bun.Tx,
	listingID int64,
	delta int32,
	orderID int64,
	operatorID int64,
) error {
	refID := orderID
	return repository.InsertStockLedger(ctx, tx, &model.SpotStockLedger{
		ListingID:  listingID,
		Delta:      delta,
		Reason:     model.StockLedgerReasonOrderLock,
		RefType:    "spot_order",
		RefID:      &refID,
		OperatorID: &operatorID,
	})
}

func createPaymentBillForSpotOrder(
	ctx context.Context,
	purchaserID int64,
	goods *model.SpotGoods,
	order *model.SpotOrder,
	totalAmount int32,
) (*paymentv1.Bill, error) {
	billResp, err := client.PaymentInternalServiceClient.CreateBillForOrder(
		ctx,
		connect.NewRequest(&paymentv1.CreateBillForOrderRequest{
			SourceType:  "spot_order",
			SourceId:    order.ID,
			PayerId:     purchaserID,
			PayeeId:     goods.SellerID,
			AmountCents: totalAmount,
		}),
	)
	if err != nil {
		log.Error().
			Err(err).
			Int64("spot_order_id", order.ID).
			Msg("failed to create payment bill")
		return nil, err
	}
	if billResp.Msg.Bill == nil || billResp.Msg.Bill.Id <= 0 {
		log.Error().Int64("spot_order_id", order.ID).Msg("payment service returned empty bill")
		return nil, spotInternalError()
	}
	return billResp.Msg.Bill, nil
}

func attachPaymentBillToSpotOrder(
	ctx context.Context,
	tx bun.Tx,
	orderID int64,
	billID int64,
) (*model.SpotOrder, error) {
	order, err := repository.AttachPaymentBill(ctx, tx, orderID, billID)
	if err != nil {
		log.Error().
			Err(err).
			Int64("spot_order_id", orderID).
			Msg("failed to attach payment bill")
		return nil, spotInternalError()
	}
	return order, nil
}

func getProductTemplate(
	ctx context.Context,
	productTemplateID int64,
) (*catalogv1.ProductTemplate, error) {
	resp, err := client.CatalogInternalServiceClient.GetProductTemplate(
		ctx,
		connect.NewRequest(&catalogv1.GetProductTemplateRequest{
			ProductTemplateId: productTemplateID,
		}),
	)
	if err != nil {
		log.Error().
			Err(err).
			Int64("product_template_id", productTemplateID).
			Msg("failed to get product template")
		return nil, err
	}
	if resp.Msg.ProductTemplate == nil {
		log.Error().
			Int64("product_template_id", productTemplateID).
			Msg("catalog service returned empty product template")
		return nil, spotInternalError()
	}
	return resp.Msg.ProductTemplate, nil
}

func getStore(ctx context.Context, storeID int64) (*catalogv1.Store, error) {
	resp, err := client.CatalogInternalServiceClient.GetStore(
		ctx,
		connect.NewRequest(&catalogv1.GetStoreRequest{
			StoreId: storeID,
		}),
	)
	if err != nil {
		log.Error().Err(err).Int64("store_id", storeID).Msg("failed to get store")
		return nil, err
	}
	if resp.Msg.Store == nil {
		log.Error().Int64("store_id", storeID).Msg("catalog service returned empty store")
		return nil, spotInternalError()
	}
	return resp.Msg.Store, nil
}

func spotOrderToDetail(
	order *model.SpotOrder,
	store *catalogv1.Store,
	product *catalogv1.ProductTemplate,
	bill *paymentv1.Bill,
) *spotv1.SpotOrderDetail {
	return &spotv1.SpotOrderDetail{
		Id:               order.ID,
		OrderNo:          order.OrderNo,
		Store:            store,
		ProductSnapshot:  product,
		Quantity:         order.Quantity,
		UnitPriceCents:   order.UnitPriceCents,
		TotalAmountCents: order.TotalAmountCents,
		BillId:           derefInt64(order.PaymentBillID),
		Status:           modelStatusToProto(order.Status),
		CreatedAt:        timestamppb.New(order.CreatedAt),
		Bill:             bill,
		PaidAt:           timeToProto(order.PaidAt),
		CompletedAt:      timeToProto(order.CompletedAt),
		CancelledAt:      timeToProto(order.CancelledAt),
	}
}

func buildSpotOrderDetail(
	ctx context.Context,
	record *repository.SpotOrderRecord,
) (*spotv1.SpotOrderDetail, error) {
	store, err := getStore(ctx, record.StoreID)
	if err != nil {
		return nil, err
	}

	seller, err := getUser(ctx, record.SellerID)
	if err != nil {
		return nil, err
	}

	bill, err := getBill(ctx, record.PaymentBillID)
	if err != nil {
		return nil, err
	}

	return spotOrderRecordToDetail(*record, store, seller, bill), nil
}

func spotOrderRecordToBrief(
	record repository.SpotOrderRecord,
	store *catalogv1.Store,
) *spotv1.SpotOrderBrief {
	return &spotv1.SpotOrderBrief{
		Id:               record.ID,
		OrderNo:          record.OrderNo,
		Store:            store,
		ProductSnapshot:  productSnapshotFromRecord(record),
		Quantity:         record.Quantity,
		UnitPriceCents:   record.UnitPriceCents,
		TotalAmountCents: record.TotalAmountCents,
		BillId:           derefInt64(record.PaymentBillID),
		Status:           modelStatusToProto(record.Status),
		CreatedAt:        timestamppb.New(record.CreatedAt),
	}
}

func spotOrderRecordToDetail(
	record repository.SpotOrderRecord,
	store *catalogv1.Store,
	seller *userv1.UserInfo,
	bill *paymentv1.Bill,
) *spotv1.SpotOrderDetail {
	return &spotv1.SpotOrderDetail{
		Id:               record.ID,
		OrderNo:          record.OrderNo,
		Store:            store,
		ProductSnapshot:  productSnapshotFromRecord(record),
		Quantity:         record.Quantity,
		UnitPriceCents:   record.UnitPriceCents,
		TotalAmountCents: record.TotalAmountCents,
		BillId:           derefInt64(record.PaymentBillID),
		Status:           modelStatusToProto(record.Status),
		CreatedAt:        timestamppb.New(record.CreatedAt),
		Seller:           seller,
		Bill:             bill,
		PaidAt:           timeToProto(record.PaidAt),
		CompletedAt:      timeToProto(record.CompletedAt),
		CancelledAt:      timeToProto(record.CancelledAt),
	}
}

func productSnapshotFromRecord(record repository.SpotOrderRecord) *catalogv1.ProductTemplate {
	return &catalogv1.ProductTemplate{
		Id:           record.ProductTemplateID,
		Title:        record.TitleSnapshot,
		Description:  record.DescriptionSnapshot,
		PriceCents:   record.UnitPriceCents,
		StoreId:      record.StoreID,
		MainImageUrl: record.ImageURLSnapshot,
	}
}

func getCachedStore(
	ctx context.Context,
	cache map[int64]*catalogv1.Store,
	storeID int64,
) (*catalogv1.Store, error) {
	if store, ok := cache[storeID]; ok {
		return store, nil
	}
	store, err := getStore(ctx, storeID)
	if err != nil {
		return nil, err
	}
	cache[storeID] = store
	return store, nil
}

func getUser(ctx context.Context, userID int64) (*userv1.UserInfo, error) {
	resp, err := client.UserInternalServiceClient.GetUsers(
		ctx,
		connect.NewRequest(&userv1.GetUsersRequest{
			UserIds: []int64{userID},
		}),
	)
	if err != nil {
		log.Error().Err(err).Int64("user_id", userID).Msg("failed to get user")
		return nil, err
	}
	for _, user := range resp.Msg.Users {
		if user.Id == userID {
			return user, nil
		}
	}
	log.Error().Int64("user_id", userID).Msg("user service returned empty user")
	return nil, spotInternalError()
}

func getBill(ctx context.Context, billID *int64) (*paymentv1.Bill, error) {
	if billID == nil || *billID <= 0 {
		return nil, nil
	}
	resp, err := client.BillServiceClient.GetBill(ctx, connect.NewRequest(&paymentv1.GetBillRequest{
		BillId: *billID,
	}))
	if err != nil {
		log.Error().Err(err).Int64("bill_id", *billID).Msg("failed to get bill")
		return nil, err
	}
	return resp.Msg.Bill, nil
}

func modelStatusToProto(status model.SpotOrderStatus) spotv1.SpotOrderStatus {
	switch status {
	case model.SpotOrderStatusPendingPayment:
		return spotv1.SpotOrderStatus_SPOT_ORDER_STATUS_PENDING_PAYMENT
	case model.SpotOrderStatusPaid:
		return spotv1.SpotOrderStatus_SPOT_ORDER_STATUS_PAID
	case model.SpotOrderStatusCompleted:
		return spotv1.SpotOrderStatus_SPOT_ORDER_STATUS_COMPLETED
	case model.SpotOrderStatusCancelled:
		return spotv1.SpotOrderStatus_SPOT_ORDER_STATUS_CANCELLED
	default:
		return spotv1.SpotOrderStatus_SPOT_ORDER_STATUS_UNSPECIFIED
	}
}

func protoStatusToModel(status spotv1.SpotOrderStatus) (model.SpotOrderStatus, bool) {
	switch status {
	case spotv1.SpotOrderStatus_SPOT_ORDER_STATUS_PENDING_PAYMENT:
		return model.SpotOrderStatusPendingPayment, true
	case spotv1.SpotOrderStatus_SPOT_ORDER_STATUS_PAID:
		return model.SpotOrderStatusPaid, true
	case spotv1.SpotOrderStatus_SPOT_ORDER_STATUS_COMPLETED:
		return model.SpotOrderStatusCompleted, true
	case spotv1.SpotOrderStatus_SPOT_ORDER_STATUS_CANCELLED:
		return model.SpotOrderStatusCancelled, true
	default:
		return "", false
	}
}

func parsePerspective(perspective spotv1.SpotGoodsPerspective) (string, error) {
	switch perspective {
	case spotv1.SpotGoodsPerspective_SPOT_GOODS_PERSPECTIVE_PURCHASER:
		return "purchaser", nil
	case spotv1.SpotGoodsPerspective_SPOT_GOODS_PERSPECTIVE_SELLER:
		return "seller", nil
	default:
		return "", connect.NewError(connect.CodeInvalidArgument, ErrInvalidCreateSpotOrdersRequest)
	}
}

func normalizePage(page int32, pageSize int32) (int32, int32) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	return page, pageSize
}

func canReadSpotOrder(userID int64, record *repository.SpotOrderRecord) bool {
	return record.PurchaserID == userID || record.SellerID == userID
}

func checkedTotalAmount(unitPriceCents int32, quantity int32) (int32, error) {
	total := int64(unitPriceCents) * int64(quantity)
	if total < math.MinInt32 || total > math.MaxInt32 {
		return 0, fmt.Errorf("total_amount_cents overflows int32")
	}
	return int32(total), nil
}

func checkedInt32(value int, fieldName string) (int32, error) {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, fmt.Errorf("%s overflows int32", fieldName)
	}
	return int32(value), nil
}

func newSpotOrderNo() (string, error) {
	return idgen.NewOrderNo(spotOrderNoPrefix)
}

func derefInt64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func timeToProto(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

func spotInternalError() error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_SpotError{
		SpotError: &spotv1.SpotError{
			Code: spotv1.SpotErrorCode_SPOT_ERROR_CODE_INTERNAL_ERROR,
		},
	}, "")
}
