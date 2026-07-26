package service

import (
	"context"
	"time"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
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
	DistributedQuantity     int32
	ServiceFeePerUnitCents  int32
	EstimatedUnitPriceCents int32
	ErrandDemandItemID      int64
	ProductAmountCents      int32
	ServiceFeeAmountCents   int32
	PackagingFeeShareCents  int32
	SubtotalCents           int32
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
	Bill                    *paymentv1.Bill
	ProductAmountCents      int32
	ServiceFeeAmountCents   int32
	PackagingFeeShareCents  int32
	TotalAmountCents        int32
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

	assignByItem, err := loadAssignments(ctx, itemIDs)
	if err != nil {
		return nil, err
	}
	taskInfo, err := loadTaskInfo(ctx, demand.TaskID)
	if err != nil {
		return nil, err
	}
	store, storeErr := client.GetStore(ctx, demand.StoreID)
	if storeErr != nil {
		log.Warn().Err(storeErr).Int64("store_id", demand.StoreID).Msg("get store failed")
	}
	productMap := fetchProducts(ctx, productIDSet)
	captainInfo := loadCaptain(ctx, taskInfo.Task)
	billID := findBillID(assignByItem)
	bill, err := loadBuyerOrderBill(ctx, billID)
	if err != nil {
		return nil, err
	}
	packagingShareCents, err := loadBuyerPackagingShare(ctx, taskInfo.Task)
	if err != nil {
		return nil, err
	}

	productItems, originCents, originServiceCents, amountSummary, err := buildProductItems(
		items,
		assignByItem,
		taskInfo,
		productMap,
		packagingShareCents,
	)
	if err != nil {
		return nil, err
	}
	if err := ensureBuyerOrderBillAmountMatches(bill, amountSummary.TotalAmountCents); err != nil {
		return nil, err
	}

	var totalActualAmountCents *int32
	if taskInfo.Task != nil {
		actual := amountSummary.ProductAmountCents
		totalActualAmountCents = &actual
	}

	return &BuyerOrderDetail{
		ErrandDemandID:          demand.ID,
		StoreID:                 demand.StoreID,
		CreatedAt:               demand.CreatedAt,
		Status:                  demand.Status,
		ProductItems:            productItems,
		TotalOriginAmountCents:  originCents,
		TotalActualAmountCents:  totalActualAmountCents,
		TotalServiceFeeCents:    originServiceCents,
		StoreInfo:               store,
		CaptainInfo:             captainInfo,
		PaymentBillID:           billID,
		Bill:                    bill,
		ProductAmountCents:      amountSummary.ProductAmountCents,
		ServiceFeeAmountCents:   amountSummary.ServiceFeeAmountCents,
		PackagingFeeShareCents:  amountSummary.PackagingFeeShareCents,
		TotalAmountCents:        amountSummary.TotalAmountCents,
		Deadline:                demand.Deadline,
		ShoppingStartAt:         demand.ShoppingStartAt,
		ShoppingCompletedAt:     demand.ShoppingCompletedAt,
		DistributionCompletedAt: demand.DistributionCompletedAt,
		PaymentCompletedAt:      demand.PaymentCompletedAt,
		CancelledAt:             demand.CancelledAt,
	}, nil
}

func loadAssignments(ctx context.Context, itemIDs []int64) (map[int64]*model.ErrandTaskAssignment, error) {
	assignments, err := repository.GetAssignmentsByDemandItemIDs(ctx, itemIDs)
	if err != nil {
		log.Error().Err(err).Msg("get assignments failed")
		return nil, ErrInternal
	}
	m := make(map[int64]*model.ErrandTaskAssignment)
	for _, a := range assignments {
		m[a.DemandItemID] = a
	}
	return m, nil
}

type buyerTaskInfo struct {
	Task               *model.ErrandTask
	TaskItemsByID      map[int64]*model.ErrandTaskItem
	TaskItemsByProduct map[int64]*model.ErrandTaskItem
}

func loadTaskInfo(ctx context.Context, taskID *int64) (*buyerTaskInfo, error) {
	info := &buyerTaskInfo{
		TaskItemsByID:      make(map[int64]*model.ErrandTaskItem),
		TaskItemsByProduct: make(map[int64]*model.ErrandTaskItem),
	}
	if taskID == nil {
		return info, nil
	}
	task, err := repository.GetTaskByID(ctx, *taskID)
	if err != nil {
		log.Error().Err(err).Int64("task_id", *taskID).Msg("get task failed")
		return nil, ErrInternal
	}
	items, err := repository.GetTaskItemsByTaskID(ctx, *taskID)
	if err != nil {
		log.Error().Err(err).Int64("task_id", *taskID).Msg("get task items failed")
		return nil, ErrInternal
	}
	info.Task = task
	for _, ti := range items {
		info.TaskItemsByID[ti.ID] = ti
		info.TaskItemsByProduct[ti.ProductTemplateID] = ti
	}
	return info, nil
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

func loadBuyerOrderBill(ctx context.Context, billID *int64) (*paymentv1.Bill, error) {
	if billID == nil || *billID <= 0 {
		return nil, nil
	}

	billsByID, err := loadPaymentBillsByID(ctx, []int64{*billID})
	if err != nil {
		return nil, err
	}
	bill := billsByID[*billID]
	if bill == nil {
		log.Error().Int64("payment_bill_id", *billID).Msg("buyer order payment bill missing")
		return nil, ErrInternal
	}
	return bill, nil
}

func loadBuyerPackagingShare(ctx context.Context, task *model.ErrandTask) (int32, error) {
	if task == nil || task.ID <= 0 || task.PackagingFeeCents <= 0 {
		return 0, nil
	}

	payerCount, err := repository.CountTaskPaymentPayers(ctx, postgres.DB, task.ID)
	if err != nil {
		log.Error().Err(err).Int64("task_id", task.ID).Msg("count task payment payers failed")
		return 0, ErrInternal
	}
	payerCount32, err := safeInt32FromInt64(payerCount)
	if err != nil {
		return 0, err
	}
	return ceilDivide(task.PackagingFeeCents, payerCount32), nil
}

func ensureBuyerOrderBillAmountMatches(bill *paymentv1.Bill, totalAmountCents int32) error {
	if bill == nil || bill.AmountCents == totalAmountCents {
		return nil
	}

	log.Error().
		Int64("payment_bill_id", bill.Id).
		Int32("bill_amount_cents", bill.AmountCents).
		Int32("detail_total_amount_cents", totalAmountCents).
		Msg("buyer order detail amount does not match bill")
	return ErrInternal
}

type buyerOrderAmountSummary struct {
	ProductAmountCents     int32
	ServiceFeeAmountCents  int32
	PackagingFeeShareCents int32
	TotalAmountCents       int32
}

type buyerOrderPackagingSortKey struct {
	Deadline     time.Time
	TaskItemID   int64
	AssignmentID int64
}

func buildProductItems(
	items []*model.ErrandDemandItem,
	assignByItem map[int64]*model.ErrandTaskAssignment,
	taskInfo *buyerTaskInfo,
	productMap map[int64]*catalogv1.ProductTemplate,
	packagingShareCents int32,
) ([]*BuyerOrderProductItem, int32, int32, buyerOrderAmountSummary, error) {
	productItems := make([]*BuyerOrderProductItem, 0, len(items))
	var originCents int64
	var originServiceCents int64
	var productAmountCents int64
	var serviceFeeAmountCents int64
	packagingTargetIndex := -1
	var packagingTargetKey buyerOrderPackagingSortKey

	for _, item := range items {
		originCents += int64(item.EstimatedUnitPriceCents) * int64(item.Quantity)
		originServiceCents += int64(item.ServiceFeePerUnitCents) * int64(item.Quantity)

		assignment := assignByItem[item.ID]
		taskItem := taskItemForBuyerItem(item, assignment, taskInfo)
		distributedQuantity := int32(0)
		if assignment != nil {
			distributedQuantity = assignment.DistributedQuantity
		}

		actualProductAmount := int64(0)
		actualUnitPriceCents := (*int32)(nil)
		purchasedQuantity := (*int32)(nil)
		nonPurchaseReason := ""
		if taskItem != nil {
			actualUnitPriceCents = taskItem.ActualUnitPriceCents
			purchasedQuantity = taskItem.PurchasedQuantity
			nonPurchaseReason = taskItem.NonPurchaseReason
		}
		if actualUnitPriceCents != nil && distributedQuantity > 0 {
			actualProductAmount = int64(*actualUnitPriceCents) * int64(distributedQuantity)
		}
		serviceFeeAmount := int64(item.ServiceFeePerUnitCents) * int64(distributedQuantity)
		lineSubtotal := actualProductAmount + serviceFeeAmount
		lineAmounts, err := safeInt32FromInt64Values(actualProductAmount, serviceFeeAmount, lineSubtotal)
		if err != nil {
			return nil, 0, 0, buyerOrderAmountSummary{}, err
		}

		pi := &BuyerOrderProductItem{
			ProductTemplate:         productMap[item.ProductTemplateID],
			ActualUnitPriceCents:    actualUnitPriceCents,
			RequiredQuantity:        item.Quantity,
			PurchasedQuantity:       purchasedQuantity,
			NonPurchaseReason:       nonPurchaseReason,
			DistributedQuantity:     distributedQuantity,
			ServiceFeePerUnitCents:  item.ServiceFeePerUnitCents,
			EstimatedUnitPriceCents: item.EstimatedUnitPriceCents,
			ErrandDemandItemID:      item.ID,
			ProductAmountCents:      lineAmounts[0],
			ServiceFeeAmountCents:   lineAmounts[1],
			SubtotalCents:           lineAmounts[2],
		}
		if assignment != nil && distributedQuantity > 0 {
			key := buyerOrderPackagingKey(taskItem, assignment)
			if packagingTargetIndex < 0 || buyerOrderPackagingKeyLess(key, packagingTargetKey) {
				packagingTargetIndex = len(productItems)
				packagingTargetKey = key
			}
		}

		productAmountCents += actualProductAmount
		serviceFeeAmountCents += serviceFeeAmount
		productItems = append(productItems, pi)
	}

	appliedPackagingShareCents := int64(0)
	if packagingTargetIndex >= 0 && packagingShareCents > 0 {
		target := productItems[packagingTargetIndex]
		target.PackagingFeeShareCents = packagingShareCents
		targetSubtotal := int64(target.ProductAmountCents) +
			int64(target.ServiceFeeAmountCents) +
			int64(packagingShareCents)
		subtotal, err := safeInt32FromInt64(targetSubtotal)
		if err != nil {
			return nil, 0, 0, buyerOrderAmountSummary{}, err
		}
		target.SubtotalCents = subtotal
		appliedPackagingShareCents = int64(packagingShareCents)
	}

	totalAmountCents := productAmountCents + serviceFeeAmountCents + appliedPackagingShareCents
	amounts, err := safeInt32FromInt64Values(
		originCents,
		originServiceCents,
		productAmountCents,
		serviceFeeAmountCents,
		appliedPackagingShareCents,
		totalAmountCents,
	)
	if err != nil {
		return nil, 0, 0, buyerOrderAmountSummary{}, err
	}

	return productItems, amounts[0], amounts[1], buyerOrderAmountSummary{
		ProductAmountCents:     amounts[2],
		ServiceFeeAmountCents:  amounts[3],
		PackagingFeeShareCents: amounts[4],
		TotalAmountCents:       amounts[5],
	}, nil
}

func taskItemForBuyerItem(
	item *model.ErrandDemandItem,
	assignment *model.ErrandTaskAssignment,
	taskInfo *buyerTaskInfo,
) *model.ErrandTaskItem {
	if taskInfo == nil {
		return nil
	}
	if assignment != nil {
		if taskItem := taskInfo.TaskItemsByID[assignment.TaskItemID]; taskItem != nil {
			return taskItem
		}
	}
	return taskInfo.TaskItemsByProduct[item.ProductTemplateID]
}

func buyerOrderPackagingKey(
	taskItem *model.ErrandTaskItem,
	assignment *model.ErrandTaskAssignment,
) buyerOrderPackagingSortKey {
	key := buyerOrderPackagingSortKey{
		TaskItemID:   assignment.TaskItemID,
		AssignmentID: assignment.ID,
	}
	if taskItem != nil {
		key.Deadline = taskItem.Deadline
	}
	return key
}

func buyerOrderPackagingKeyLess(a, b buyerOrderPackagingSortKey) bool {
	if !a.Deadline.Equal(b.Deadline) {
		return a.Deadline.Before(b.Deadline)
	}
	if a.TaskItemID != b.TaskItemID {
		return a.TaskItemID < b.TaskItemID
	}
	return a.AssignmentID < b.AssignmentID
}
