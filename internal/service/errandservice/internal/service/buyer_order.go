package service

import (
	"context"
	"time"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/client"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/repository"
	"github.com/rs/zerolog/log"
)

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

type BuyerOrderProductItem struct {
	ProductTemplate         *catalogv1.ProductTemplate
	ActualUnitPriceCents    *int32
	RequiredQuantity        int32
	PurchasedQuantity       *int32
	NonPurchaseReason       string
	DistributedQuantity     *int32
	ServiceFeePerUnitCents  int32
	SubtotalCents           int32
	EstimatedUnitPriceCents int32
	ErrandDemandItemID      int64
}

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
			ProductTotalCount:      int32(len(items)), //nolint:gosec
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

	assignByItem := loadAssignments(ctx, itemIDs)
	taskItemByProduct, task := loadTaskInfo(ctx, demand.TaskID)
	store, storeErr := client.GetStore(ctx, demand.StoreID)
	if storeErr != nil {
		log.Warn().Err(storeErr).Int64("store_id", demand.StoreID).Msg("get store failed")
	}
	productMap := fetchProducts(ctx, productIDSet)
	captainInfo := loadCaptain(ctx, task)
	billID := findBillID(assignByItem)

	packagingShareCents, err := loadBuyerPackagingShareCents(ctx, task, requesterID)
	if err != nil {
		log.Error().Err(err).Int64("demand_id", demandID).Msg("calculate buyer packaging share failed")
		return nil, ErrInternal
	}

	productItems, originCents, actualCents, serviceCents, err := buildProductItems(
		items,
		assignByItem,
		taskItemByProduct,
		productMap,
		packagingShareCents,
	)
	if err != nil {
		log.Error().Err(err).Int64("demand_id", demandID).Msg("build buyer order product items failed")
		return nil, ErrInternal
	}

	return &BuyerOrderDetail{
		ErrandDemandID:          demand.ID,
		StoreID:                 demand.StoreID,
		CreatedAt:               demand.CreatedAt,
		Status:                  demand.Status,
		ProductItems:            productItems,
		TotalOriginAmountCents:  originCents,
		TotalActualAmountCents:  actualCents,
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

func loadAssignments(ctx context.Context, itemIDs []int64) map[int64]*model.ErrandTaskAssignment {
	assignments, err := repository.GetAssignmentsByDemandItemIDs(ctx, itemIDs)
	if err != nil {
		log.Warn().Err(err).Msg("get assignments failed")
	}
	m := make(map[int64]*model.ErrandTaskAssignment)
	for _, a := range assignments {
		m[a.DemandItemID] = a
	}
	return m
}

func loadTaskInfo(ctx context.Context, taskID *int64) (map[int64]*model.ErrandTaskItem, *model.ErrandTask) {
	if taskID == nil {
		return nil, nil
	}
	task, err := repository.GetTaskByID(ctx, postgres.DB, *taskID)
	if err != nil {
		log.Warn().Err(err).Int64("task_id", *taskID).Msg("get task failed")
		return nil, nil
	}
	items, err := repository.GetTaskItemsByTaskID(ctx, *taskID)
	if err != nil {
		log.Warn().Err(err).Int64("task_id", *taskID).Msg("get task items failed")
	}
	m := make(map[int64]*model.ErrandTaskItem)
	for _, ti := range items {
		m[ti.ProductTemplateID] = ti
	}
	return m, task
}

func loadCaptain(ctx context.Context, task *model.ErrandTask) *userv1.UserInfo {
	if task == nil {
		return nil
	}
	users, err := client.GetUsers(ctx, []int64{task.CaptainID})
	if err != nil || len(users) == 0 {
		return nil
	}
	return users[0]
}

func findBillID(assignByItem map[int64]*model.ErrandTaskAssignment) *int64 {
	for _, a := range assignByItem {
		if a.PaymentBillID != nil {
			return a.PaymentBillID
		}
	}
	return nil
}

func loadBuyerPackagingShareCents(ctx context.Context, task *model.ErrandTask, requesterID int64) (int32, error) {
	if task == nil || task.PackagingFeeCents <= 0 {
		return 0, nil
	}

	rows, err := repository.ListTaskPaymentBillAssignments(ctx, postgres.DB, task.ID)
	if err != nil {
		return 0, err
	}

	payerIDs := make(map[int64]struct{})
	requesterHasBillableItem := false
	for _, row := range rows {
		payerIDs[row.PayerID] = struct{}{}
		if row.PayerID == requesterID {
			requesterHasBillableItem = true
		}
	}
	if !requesterHasBillableItem {
		return 0, nil
	}

	payerCount, err := safeInt32FromInt(len(payerIDs))
	if err != nil {
		return 0, err
	}
	return ceilDivide(task.PackagingFeeCents, payerCount), nil
}

func buildProductItems(
	items []*model.ErrandDemandItem,
	assignByItem map[int64]*model.ErrandTaskAssignment,
	taskItemByProduct map[int64]*model.ErrandTaskItem,
	productMap map[int64]*catalogv1.ProductTemplate,
	packagingShareCents int32,
) ([]*BuyerOrderProductItem, int32, *int32, int32, error) {
	productItems := make([]*BuyerOrderProductItem, 0, len(items))
	actualTotalsReady := len(items) > 0
	packagingApplied := false
	var originCents, actualProductCents, serviceCents int64

	for _, item := range items {
		originCents += int64(item.EstimatedUnitPriceCents) * int64(item.Quantity)

		pi := &BuyerOrderProductItem{
			ProductTemplate:         productMap[item.ProductTemplateID],
			RequiredQuantity:        item.Quantity,
			ServiceFeePerUnitCents:  item.ServiceFeePerUnitCents,
			EstimatedUnitPriceCents: item.EstimatedUnitPriceCents,
			ErrandDemandItemID:      item.ID,
		}

		quantity := item.Quantity
		if a, ok := assignByItem[item.ID]; ok {
			pi.DistributedQuantity = a.DistributedQuantity
			if a.DistributedQuantity != nil {
				quantity = *a.DistributedQuantity
			} else {
				actualTotalsReady = false
			}
		} else {
			actualTotalsReady = false
		}

		actualUnitPriceCents := item.EstimatedUnitPriceCents
		if ti, ok := taskItemByProduct[item.ProductTemplateID]; ok {
			pi.ActualUnitPriceCents = ti.ActualUnitPriceCents
			pi.PurchasedQuantity = ti.PurchasedQuantity
			pi.NonPurchaseReason = ti.NonPurchaseReason
			if ti.ActualUnitPriceCents != nil {
				actualUnitPriceCents = *ti.ActualUnitPriceCents
			} else if quantity > 0 {
				actualTotalsReady = false
			}
		} else if quantity > 0 {
			actualTotalsReady = false
		}

		productAmount := int64(actualUnitPriceCents) * int64(quantity)
		serviceFeeAmount := int64(item.ServiceFeePerUnitCents) * int64(quantity)
		subtotalCents := productAmount + serviceFeeAmount
		if !packagingApplied && packagingShareCents > 0 {
			subtotalCents += int64(packagingShareCents)
			packagingApplied = true
		}

		subtotal, err := safeInt32FromInt64(subtotalCents)
		if err != nil {
			return nil, 0, nil, 0, err
		}
		pi.SubtotalCents = subtotal
		actualProductCents += productAmount
		serviceCents += serviceFeeAmount
		productItems = append(productItems, pi)
	}

	origin, err := safeInt32FromInt64(originCents)
	if err != nil {
		return nil, 0, nil, 0, err
	}
	service, err := safeInt32FromInt64(serviceCents)
	if err != nil {
		return nil, 0, nil, 0, err
	}

	var actual *int32
	if actualTotalsReady {
		actualSummary, err := safeInt32FromInt64(actualProductCents + int64(packagingShareCents))
		if err != nil {
			return nil, 0, nil, 0, err
		}
		actual = &actualSummary
	}

	return productItems, origin, actual, service, nil
}
