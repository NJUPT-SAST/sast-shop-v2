package service

import (
	"context"
	"time"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/client"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/repository"
	"github.com/rs/zerolog/log"
)

// BuyerOrderBrief 买家跑腿订单列表项。
type BuyerOrderBrief struct {
	ErrandDemandID         int64
	StoreID                int64
	CreatedAt              time.Time
	Status                 model.ErrandDemandStatus
	ProductTotalCount      int32
	TotalOriginAmountCents int32
	TotalActualAmountCents *int32
	TotalServiceFeeCents   int32
	StoreInfo              *catalogv1.Store
	ProductTemplates       []*catalogv1.ProductTemplate
}

// BuyerOrderProductItem 订单详情中的商品行。
type BuyerOrderProductItem struct {
	ProductTemplate         *catalogv1.ProductTemplate
	ActualUnitPriceCents    *int32
	RequiredQuantity        int32
	PurchasedQuantity       *int32
	NonPurchaseReason       string
	DistributedQuantity     int32
	ServiceFeePerUnitCents  int32
	EstimatedUnitPriceCents int32
	ErrandDemandItemID      int64
}

// BuyerOrderDetail 买家跑腿订单详情。
type BuyerOrderDetail struct {
	ErrandDemandID          int64
	StoreID                 int64
	CreatedAt               time.Time
	Status                  model.ErrandDemandStatus
	ProductItems            []*BuyerOrderProductItem
	TotalOriginAmountCents  int32
	TotalActualAmountCents  *int32
	TotalServiceFeeCents    int32
	StoreInfo               *catalogv1.Store
	CaptainInfo             *userv1.UserInfo
	PaymentBillID           *int64
	Deadline                time.Time
	ShoppingStartAt         *time.Time
	ShoppingCompletedAt     *time.Time
	DistributionCompletedAt *time.Time
	PaymentCompletedAt      *time.Time
	CancelledAt             *time.Time
}

// GetBuyerOrderBriefList 分页查询买家跑腿订单列表。
func GetBuyerOrderBriefList(
	ctx context.Context,
	requesterID int64,
	storeID *int64,
	status *model.ErrandDemandStatus,
	page, pageSize int32,
) ([]*BuyerOrderBrief, int, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	demands, totalCount, err := repository.GetDemandsByRequester(ctx, requesterID, storeID, status, page, pageSize)
	if err != nil {
		log.Error().Err(err).Msg("get demands by requester failed")
		return nil, 0, ErrInternal
	}
	if len(demands) == 0 {
		return nil, 0, nil
	}

	demandIDs := make([]int64, 0, len(demands))
	for _, d := range demands {
		demandIDs = append(demandIDs, d.ID)
	}

	allItems, err := repository.GetDemandItemsByDemandIDs(ctx, demandIDs)
	if err != nil {
		log.Error().Err(err).Msg("get demand items failed")
		return nil, 0, ErrInternal
	}

	itemsByDemand := make(map[int64][]*model.ErrandDemandItem)
	productIDSet := make(map[int64]struct{})
	storeIDSet := make(map[int64]struct{})
	for _, item := range allItems {
		itemsByDemand[item.ErrandDemandID] = append(itemsByDemand[item.ErrandDemandID], item)
		productIDSet[item.ProductTemplateID] = struct{}{}
		storeIDSet[item.StoreID] = struct{}{}
	}

	storeIDs := make([]int64, 0, len(storeIDSet))
	for sid := range storeIDSet {
		storeIDs = append(storeIDs, sid)
	}
	storeMap := fetchStores(ctx, storeIDs)
	productMap := fetchProducts(ctx, productIDSet)

	results := make([]*BuyerOrderBrief, 0, len(demands))
	for _, d := range demands {
		items := itemsByDemand[d.ID]
		var originCents, serviceCents int32
		previewProducts := make([]*catalogv1.ProductTemplate, 0, 3)

		for i, item := range items {
			originCents += item.EstimatedUnitPriceCents * item.Quantity
			serviceCents += item.ServiceFeePerUnitCents * item.Quantity
			if i < 3 {
				if p, ok := productMap[item.ProductTemplateID]; ok {
					previewProducts = append(previewProducts, p)
				}
			}
		}

		results = append(results, &BuyerOrderBrief{
			ErrandDemandID:         d.ID,
			StoreID:                d.StoreID,
			CreatedAt:              d.CreatedAt,
			Status:                 d.Status,
			ProductTotalCount:      int32(len(items)),
			TotalOriginAmountCents: originCents,
			TotalServiceFeeCents:   serviceCents,
			StoreInfo:              storeMap[d.StoreID],
			ProductTemplates:       previewProducts,
		})
	}

	return results, totalCount, nil
}

func fetchStores(ctx context.Context, ids []int64) map[int64]*catalogv1.Store {
	m := make(map[int64]*catalogv1.Store)
	for _, id := range ids {
		store, err := client.GetStore(ctx, id)
		if err != nil {
			log.Warn().Err(err).Int64("store_id", id).Msg("get store failed, skip")
			continue
		}
		m[id] = store
	}
	return m
}

func fetchProducts(ctx context.Context, idSet map[int64]struct{}) map[int64]*catalogv1.ProductTemplate {
	m := make(map[int64]*catalogv1.ProductTemplate)
	for id := range idSet {
		p, err := client.GetProductTemplate(ctx, id)
		if err != nil {
			log.Warn().Err(err).Int64("product_id", id).Msg("get product template failed, skip")
			continue
		}
		m[id] = p
	}
	return m
}

// GetBuyerOrderDetail 查询买家跑腿订单详情。
func GetBuyerOrderDetail(ctx context.Context, requesterID, demandID int64) (*BuyerOrderDetail, error) {
	demand, err := repository.GetDemandByID(ctx, demandID)
	if err != nil {
		log.Error().Err(err).Int64("demand_id", demandID).Msg("get demand failed")
		return nil, ErrInternal
	}
	if demand.RequesterID != requesterID {
		log.Warn().Int64("demand_id", demandID).Msg("demand does not belong to requester")
		return nil, ErrInternal
	}

	items, err := repository.GetDemandItemsByDemandIDs(ctx, []int64{demandID})
	if err != nil {
		log.Error().Err(err).Int64("demand_id", demandID).Msg("get demand items failed")
		return nil, ErrInternal
	}

	itemIDs := make([]int64, 0, len(items))
	productIDSet := make(map[int64]struct{})
	for _, item := range items {
		itemIDs = append(itemIDs, item.ID)
		productIDSet[item.ProductTemplateID] = struct{}{}
	}

	assignments, _ := repository.GetAssignmentsByDemandItemIDs(ctx, itemIDs)
	assignByItem := make(map[int64]*model.ErrandTaskAssignment)
	for _, a := range assignments {
		assignByItem[a.DemandItemID] = a
	}

	var task *model.ErrandTask
	var taskItems []*model.ErrandTaskItem
	if demand.TaskID != nil {
		task, _ = repository.GetTaskByID(ctx, *demand.TaskID)
		taskItems, _ = repository.GetTaskItemsByTaskID(ctx, *demand.TaskID)
	}
	taskItemByProduct := make(map[int64]*model.ErrandTaskItem)
	for _, ti := range taskItems {
		taskItemByProduct[ti.ProductTemplateID] = ti
	}

	store, _ := client.GetStore(ctx, demand.StoreID)
	productMap := fetchProducts(ctx, productIDSet)

	var captainInfo *userv1.UserInfo
	if task != nil {
		users, err := client.GetUsers(ctx, []int64{task.CaptainID})
		if err == nil && len(users) > 0 {
			captainInfo = users[0]
		}
	}

	var billID *int64
	for _, a := range assignments {
		if a.PaymentBillID != nil {
			billID = a.PaymentBillID
			break
		}
	}

	productItems := make([]*BuyerOrderProductItem, 0, len(items))
	var originCents, serviceCents int32

	for _, item := range items {
		originCents += item.EstimatedUnitPriceCents * item.Quantity
		serviceCents += item.ServiceFeePerUnitCents * item.Quantity

		pi := &BuyerOrderProductItem{
			ProductTemplate:         productMap[item.ProductTemplateID],
			RequiredQuantity:        item.Quantity,
			ServiceFeePerUnitCents:  item.ServiceFeePerUnitCents,
			EstimatedUnitPriceCents: item.EstimatedUnitPriceCents,
			ErrandDemandItemID:      item.ID,
		}

		if a, ok := assignByItem[item.ID]; ok {
			pi.DistributedQuantity = a.DistributedQuantity
		}

		if ti, ok := taskItemByProduct[item.ProductTemplateID]; ok {
			pi.ActualUnitPriceCents = ti.ActualUnitPriceCents
			pi.PurchasedQuantity = ti.PurchasedQuantity
			pi.NonPurchaseReason = ti.NonPurchaseReason
		}

		productItems = append(productItems, pi)
	}

	return &BuyerOrderDetail{
		ErrandDemandID:          demand.ID,
		StoreID:                 demand.StoreID,
		CreatedAt:               demand.CreatedAt,
		Status:                  demand.Status,
		ProductItems:            productItems,
		TotalOriginAmountCents:  originCents,
		TotalServiceFeeCents:    serviceCents,
		StoreInfo:               store,
		CaptainInfo:             captainInfo,
		PaymentBillID:           billID,
		Deadline:                demand.Deadline,
		ShoppingStartAt:         demand.ShoppingStartAt,
		ShoppingCompletedAt:     demand.ShoppingCompletedAt,
		DistributionCompletedAt: demand.DistributionCompletedAt,
		PaymentCompletedAt:      demand.PaymentCompletedAt,
		CancelledAt:             demand.CancelledAt,
	}, nil
}
