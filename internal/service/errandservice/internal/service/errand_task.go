package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/feishu"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/client"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/repository"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// 按截止时间和相同商品类型聚合
type taskItemGroupKey struct {
	ProductTemplateID int64
	Deadline          time.Time
}

// 按商品id和截止时间分组结果
type taskItemGroup struct {
	Snapshot         repository.ProductSnapshotRow
	RequiredQuantity int32
	Rows             []repository.SelectedDemandItemRow
}

// 记录团长选中的需求
type createTaskSelection struct {
	SelectedUpdatedAt map[int64]time.Time
	SelectedIDs       []int64
}

// 数据加载结果
type createTaskLoadResult struct {
	Now        time.Time
	Rows       []repository.SelectedDemandItemRow           // 完整列表，用于遍历和统计
	DemandRows map[int64][]repository.SelectedDemandItemRow // 按demandID分组，用于业务处理
	Snapshots  map[int64]repository.ProductSnapshotRow
}

var (
	ErrInvalidDemandItem   = errors.New("invalid demand item")  // 传参错误
	ErrConcurrencyConflict = errors.New("concurrency conflict") // 乐观锁冲突
	ErrStoreMismatch       = errors.New("store mismatch")       // 所选需求项与门店不一致
	ErrDemandItemNotOpen   = errors.New("demand item not open") // 任务已关闭
)

func CreateTask(ctx context.Context, captainID int64, req *errandv1.CreateTaskRequest) (int64, error) {
	if captainID <= 0 {
		return 0, connect.NewError(connect.CodeUnauthenticated, errors.New("missing captain id"))
	}

	if req == nil || req.StoreId <= 0 || len(req.DemandItems) == 0 {
		return 0, ErrInvalidDemandItem
	}

	// 解析请求,
	selection, err := buildCreateTaskSelection(req)
	if err != nil {
		return 0, err
	}

	var taskID int64
	err = repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		// 根据店铺id和选项id加载数据:demand_row,从 catalog 读取商品模板
		loaded, err := loadCreateTaskData(ctx, tx, req.StoreId, selection)
		if err != nil {
			return err
		}
		// 2. 创建 errand_task，状态为 shopping
		task, err := createShoppingTask(ctx, tx, captainID, req.StoreId)
		if err != nil {
			return err
		}
		taskID = task.ID
		// 按 product_template_id + deadline 聚合被选中的 demand_item，写入 errand_task_item，此时从 catalog 读取商品模板，并固化 title_snapshot、image_url_snapshot 等任务快照字段
		taskItemIDByDemandItemID, err := createGroupedTaskItems(ctx, tx, task.ID, loaded.Rows, loaded.Snapshots)
		if err != nil {
			return err
		}
		// 2. 为每个 demand_item 写入 errand_task_assignment，并将 demand_item.status 更新为 shopping
		if err := createTaskAssignments(ctx, tx, task.ID, loaded.Rows, taskItemIDByDemandItemID); err != nil {
			return err
		}
		// 同步需求状态，标记已接单shopping
		if err := syncSelectedDemandStatus(ctx, tx, task.ID, loaded.Now, loaded.DemandRows); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return taskID, nil
}

func buildCreateTaskSelection(req *errandv1.CreateTaskRequest) (*createTaskSelection, error) {
	selectedUpdatedAt := make(map[int64]time.Time, len(req.DemandItems))
	selectedIDs := make([]int64, 0, len(req.DemandItems))

	for i, item := range req.DemandItems {
		if item == nil || item.ErrandDemandItemId <= 0 || item.UpdatedAt == nil || !item.UpdatedAt.IsValid() {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("demand_items[%d] is invalid", i))
		}
		// 防止前端传两次同一需求
		if _, dup := selectedUpdatedAt[item.ErrandDemandItemId]; dup {
			return nil, ErrInvalidDemandItem
		}

		selectedUpdatedAt[item.ErrandDemandItemId] = item.UpdatedAt.AsTime().UTC()
		selectedIDs = append(selectedIDs, item.ErrandDemandItemId)
	}

	return &createTaskSelection{
		SelectedUpdatedAt: selectedUpdatedAt,
		SelectedIDs:       selectedIDs,
	}, nil
}

func loadCreateTaskData(
	ctx context.Context,
	tx bun.Tx,
	storeID int64,
	selection *createTaskSelection,
) (*createTaskLoadResult, error) {
	// 锁定被选择的 demand_item
	rows, err := repository.LoadSelectedDemandItemsForUpdate(ctx, tx, selection.SelectedIDs)
	if err != nil {
		log.Error().Err(err).Msg("failed to load selected demand items")
		return nil, newErrandInternalError("")
	}
	if len(rows) != len(selection.SelectedIDs) {
		return nil, ErrInvalidDemandItem
	}

	now := time.Now().UTC()
	demandRows := make(map[int64][]repository.SelectedDemandItemRow) // 按用户需求分组
	productIDsSet := make(map[int64]struct{}, len(rows))             // 收集商品id，去重后批量加载商品快照

	for _, row := range rows {
		// 校验其仍处于 open 状态，并基于 updated_at 做并发校验
		if err := validateSelectedDemandItemRow(row, storeID, selection.SelectedUpdatedAt, now); err != nil {
			return nil, err
		}

		demandRows[row.DemandID] = append(demandRows[row.DemandID], row) // 按demandID分组
		productIDsSet[row.ProductTemplateID] = struct{}{}                // map键具有唯一性，自动去重
	}
	// 根据店铺id和商品id加载和校验商品快照
	snapshots, err := loadValidatedSnapshots(ctx, tx, storeID, productIDsSet)
	if err != nil {
		return nil, err
	}

	return &createTaskLoadResult{
		Now:        now,
		Rows:       rows,
		DemandRows: demandRows,
		Snapshots:  snapshots,
	}, nil
}

func validateSelectedDemandItemRow(
	row repository.SelectedDemandItemRow,
	storeID int64,
	selectedUpdatedAt map[int64]time.Time,
	now time.Time,
) error {
	if row.StoreID != storeID {
		return ErrStoreMismatch
	}
	if row.DemandStatus != model.ErrandDemandStatusOpen || row.DemandItemStatus != model.ErrandDemandItemStatusOpen {
		return ErrDemandItemNotOpen
	}
	if !row.DemandItemUpdatedAt.UTC().Equal(selectedUpdatedAt[row.DemandItemID]) {
		return ErrConcurrencyConflict
	}
	if !row.Deadline.After(now) {
		return ErrInvalidDemandItem
	}

	return nil
}

func loadValidatedSnapshots(
	ctx context.Context,
	tx bun.Tx,
	storeID int64,
	productIDsSet map[int64]struct{},
) (map[int64]repository.ProductSnapshotRow, error) {
	productIDs := make([]int64, 0, len(productIDsSet))
	// 遍历map所有的key
	for id := range productIDsSet {
		productIDs = append(productIDs, id)
	}

	snapshots, err := repository.LoadProductSnapshots(ctx, tx, productIDs)
	if err != nil {
		log.Error().Err(err).Msg("failed to load product snapshots")
		return nil, newErrandInternalError("")
	}

	if len(snapshots) != len(productIDs) {
		return nil, newErrandInternalError("")
	}

	for _, snap := range snapshots {
		if snap.StoreID != storeID {
			return nil, ErrStoreMismatch
		}
	}

	return snapshots, nil
}

func createShoppingTask(ctx context.Context, tx bun.Tx, captainID, storeID int64) (*model.ErrandTask, error) {
	task := &model.ErrandTask{
		TaskNo:    generateTaskNo(),
		CaptainID: captainID,
		StoreID:   storeID,
		Status:    model.ErrandTaskStatusShopping,
	}
	if err := repository.CreateTask(ctx, tx, task); err != nil {
		log.Error().Err(err).Msg("failed to create task")
		return nil, newErrandInternalError("")
	}

	return task, nil
}

// 按 product_template_id + deadline 聚合被选中的 demand_item
func buildTaskItemGroups(
	rows []repository.SelectedDemandItemRow,
	snapshots map[int64]repository.ProductSnapshotRow,
) map[taskItemGroupKey]*taskItemGroup {
	grouped := make(map[taskItemGroupKey]*taskItemGroup)

	for _, row := range rows {
		key := taskItemGroupKey{
			ProductTemplateID: row.ProductTemplateID,
			Deadline:          row.Deadline,
		}
		group, ok := grouped[key]
		if !ok {
			group = &taskItemGroup{
				Snapshot: snapshots[row.ProductTemplateID],
			}
			grouped[key] = group
		}
		group.RequiredQuantity += row.RequiredQuantity
		group.Rows = append(group.Rows, row)
	}

	return grouped
}

func createGroupedTaskItems(
	ctx context.Context,
	tx bun.Tx,
	taskID int64,
	rows []repository.SelectedDemandItemRow,
	snapshots map[int64]repository.ProductSnapshotRow,
) (map[int64]int64, error) {
	//按 product_template_id + deadline 聚合被选中的 demand_item
	grouped := buildTaskItemGroups(rows, snapshots)
	taskItemIDByDemandItemID := make(map[int64]int64, len(rows))

	for key, group := range grouped {
		//写入 errand_task_item
		taskItem := &model.ErrandTaskItem{
			TaskID:              taskID,
			ProductTemplateID:   key.ProductTemplateID,
			TitleSnapshot:       group.Snapshot.Title,
			DescriptionSnapshot: group.Snapshot.Description,
			ImageURLSnapshot:    group.Snapshot.MainImageURL,
			RequiredQuantity:    group.RequiredQuantity,
			Deadline:            key.Deadline,
		}
		if err := repository.CreateTaskItem(ctx, tx, taskItem); err != nil {
			log.Error().Err(err).Msg("failed to create task item")
			return nil, newErrandInternalError("")
		}
		// 按demandItemID分组
		for _, row := range group.Rows {
			taskItemIDByDemandItemID[row.DemandItemID] = taskItem.ID
		}
	}

	return taskItemIDByDemandItemID, nil
}

func createTaskAssignments(
	ctx context.Context,
	tx bun.Tx,
	taskID int64,
	rows []repository.SelectedDemandItemRow,
	taskItemIDByDemandItemID map[int64]int64,
) error {
	assigniments := make([]*model.ErrandTaskAssignment, 0, len(rows))
	for _, row := range rows {
		assigniments = append(assigniments, &model.ErrandTaskAssignment{
			TaskID:                 taskID,
			TaskItemID:             taskItemIDByDemandItemID[row.DemandItemID],
			DemandItemID:           row.DemandItemID,
			PurchaserID:            row.RequesterID,
			ServiceFeePerUnitCents: row.ServiceFeePerUnitCents,
		})
	}

	if err := repository.CreateTaskAssigniments(ctx, tx, assigniments); err != nil {
		log.Error().Err(err).Msg("failed to create task assigniments")
		return newErrandInternalError("")
	}

	return nil
}

func syncSelectedDemandStatus(
	ctx context.Context,
	tx bun.Tx,
	taskID int64,
	now time.Time,
	demandRows map[int64][]repository.SelectedDemandItemRow,
) error {
	demandIDs := make([]int64, 0, len(demandRows))
	for demandID := range demandRows {
		demandIDs = append(demandIDs, demandID)
	}
	// 批量查询每个demand下的总商品数
	itemCountByDemandID, err := repository.LoadDemandItemCounts(ctx, tx, demandIDs)
	if err != nil {
		log.Error().Err(err).Msg("failed to count demand items")
		return newErrandInternalError("")
	}
	// 逐个处理每个demand
	for demandID, selectedRows := range demandRows {
		if err := syncSingleDemand(ctx, tx, taskID, demandID, selectedRows, itemCountByDemandID, now); err != nil {
			return err
		}
	}

	return nil
}

func syncSingleDemand(
	ctx context.Context,
	tx bun.Tx,
	taskID int64,
	demandID int64,
	selectedRows []repository.SelectedDemandItemRow,
	itemCountByDemandID map[int64]int,
	now time.Time,
) error {
	totalCount, ok := itemCountByDemandID[demandID]
	if !ok || totalCount == 0 {
		return newErrandInternalError("demand item count missing")
	}

	selectedItemIDs := demandItemIDs(selectedRows)
	// 如果团长选择了某个 demand 下的全部商品，原 demand 直接进入 shopping，并关联新 task
	if totalCount == len(selectedRows) {
		if err := repository.UpdateDemandToShopping(ctx, tx, demandID, taskID, now); err != nil {
			log.Error().Err(err).Msg("failed to update full-selected demand")
			return newErrandInternalError("")
		}
		if err := repository.UpdateDemandItemsToShopping(ctx, tx, selectedItemIDs, now); err != nil {
			log.Error().Err(err).Msg("failed to update full-selected demand")
			return newErrandInternalError("")
		}

		return nil
	}
	//如果团长只选择了部分商品，系统创建一个新的 demand，状态为 shopping,task_id 指向新 task
	base := selectedRows[0]
	taskIDCopy := taskID
	demandIDCopy := demandID
	splitDemand := &model.ErrandDemand{
		RequesterID:       base.RequesterID,
		StoreID:           base.StoreID,
		Status:            model.ErrandDemandStatusShopping,
		Deadline:          base.Deadline,
		TaskID:            &taskIDCopy,
		SplitFromDemandID: &demandIDCopy,
		ShoppingStartAt:   &now,
	}
	//并将被选中的 demand_item 移入该新 demand；原 demand 仅保留未被选中的 demand_item，状态继续保持 open
	if err := repository.CreateDemand(ctx, tx, splitDemand); err != nil {
		log.Error().Err(err).Msg("failed to create split demand")
		return newErrandInternalError("")
	}

	if err := repository.MoveDemandItemsToDemandAndShopping(ctx, tx, selectedItemIDs, splitDemand.ID, now); err != nil {
		log.Error().Err(err).Msg("failed to move selected items to split demand")
		return newErrandInternalError("")
	}

	if err := repository.TouchDemandUpdatedAt(ctx, tx, demandID, now); err != nil {
		log.Error().Err(err).Msg("failed to touch original demand")
		return newErrandInternalError("")
	}

	return nil
}

func demandItemIDs(rows []repository.SelectedDemandItemRow) []int64 {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.DemandItemID)
	}

	return ids
}

func generateTaskNo() string {
	ts := time.Now().Format("20060102150405")
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "ET" + ts + fmt.Sprintf("%06d", time.Now().UnixNano()%1_000_000)
	}
	return "ET" + ts + fmt.Sprintf("%06d", n.Int64())
}

func newErrandInternalError(msg string) error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_ErrandError{
		ErrandError: &errandv1.ErrandError{
			Code: errandv1.ErrandErrorCode_ERRAND_ERROR_CODE_INTERNAL_ERROR,
		},
	}, msg)
}

// 获取采购中的跑腿任务详情
func GetShoppingTaskDetail(
	ctx context.Context,
	captainID int64,
	req *errandv1.GetShoppingTaskDetailRequest,
) (*errandv1.GetShoppingTaskDetailResponse, error) {
	if captainID <= 0 {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing captain id"))
	}

	if req == nil || req.ErrandTaskId <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid errand task id"))
	}

	header, err := repository.GetShoppingTaskHeader(ctx, postgres.DB, req.ErrandTaskId, captainID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("shopping task not found"))
		}
		log.Error().
			Err(err).
			Int64("errand_task_id", req.ErrandTaskId).
			Int64("captain_id", captainID).
			Msg("failde to load shopping task header")
		return nil, newErrandInternalError("")
	}

	if header.Status != model.ErrandTaskStatusShopping {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("task is not in shopping status"))
	}

	itemRows, err := repository.ListShoppingTaskItems(ctx, postgres.DB, header.TaskID)
	if err != nil {
		log.Error().Err(err).Int64("errand_task_id", header.TaskID).Msg("failed to load shopping task items")
		return nil, newErrandInternalError("")
	}
	taskItems := make([]*errandv1.ErrandTaskItem, 0, len(itemRows))
	for _, row := range itemRows {
		taskItems = append(taskItems, shoppingTaskItemRowToProto(row, header.StoreID))
	}

	return &errandv1.GetShoppingTaskDetailResponse{
		ErrandTaskId: header.TaskID,
		StoreId:      header.StoreID,
		StoreName:    header.StoreName,
		TaskItems:    taskItems,
	}, nil
}

func shoppingTaskItemRowToProto(row repository.ShoppingTaskItemRow, storeID int64) *errandv1.ErrandTaskItem {
	taskItem := &errandv1.ErrandTaskItem{
		Id: row.TaskItemID,
		ProductSnapshot: &catalogv1.ProductTemplate{
			Id:           row.ProductTemplateID,
			Title:        row.TitleSnapshot,
			Description:  row.DescriptionSnapshot,
			PriceCents:   row.ProductPriceCents,
			StoreId:      storeID,
			MainImageUrl: row.ImageURLSnapshot,
		},
		RequiredQuantity:     row.RequiredQuantity,
		ActualUnitPriceCents: *row.ActualUnitPriceCents,
		UpdatedAt:            timestamppb.New(row.UpdatedAt),
	}
	if row.PurchasedQuantity != nil {
		purchasedQuantity := *row.PurchasedQuantity
		taskItem.PurchasedQuantity = &purchasedQuantity
	}
	if row.NonPurchaseReason != "" {
		nonPurchaseReason := row.NonPurchaseReason
		taskItem.NonPurchaseReason = &nonPurchaseReason
	}
	return taskItem
}

// 更新采购中的跑腿任务商品项（采购中）
func SaveShoppingTaskItem(ctx context.Context, captainID int64, req *errandv1.SaveShoppingTaskItemRequest) error {
	if err := ValidateSaveRequest(ctx, captainID, req); err != nil {
		return err
	}
	return executeSaveShoppingTask(ctx, captainID, req)
}

// 基础校验
func ValidateSaveRequest(ctx context.Context, captainID int64, req *errandv1.SaveShoppingTaskItemRequest) error {
	if captainID <= 0 {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("missing captain id"))
	}
	if req == nil || req.ErrandTaskId <= 0 || req.ErrandTaskItemId <= 0 || req.ErrandTaskItemUpdatedAt == nil ||
		!req.ErrandTaskItemUpdatedAt.IsValid() {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("incalid save shopping task item request"))
	}
	return nil
}

func executeSaveShoppingTask(ctx context.Context, captainID int64, req *errandv1.SaveShoppingTaskItemRequest) error {
	expectedUpdatedAt := req.ErrandTaskItemUpdatedAt.AsTime().UTC()
	return repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		row, err := loadTaskItem(ctx, tx, req, captainID)
		if err != nil {
			return err
		}
		if row.TaskStatus != model.ErrandTaskStatusShopping {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("task is not in shopping status"))
		}
		// 基于 errand_task_item_updated_at 校验并发后
		if !row.TaskItemUpdatedAt.UTC().Equal(expectedUpdatedAt) {
			return ErrConcurrencyConflict
		}
		if req.PurchasedQuantity < 0 || req.PurchasedQuantity > row.RequiredQuantity {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid purchased quantity"))
		}
		//更新 purchased_quantity、non_purchase_reason、handled_at、updated_at
		return updateTaskItem(ctx, tx, req, expectedUpdatedAt, captainID)
	})
}

// 加载数据
func loadTaskItem(
	ctx context.Context,
	tx bun.Tx,
	req *errandv1.SaveShoppingTaskItemRequest,
	captainID int64,
) (*repository.ShoppingTaskItemForUpdateRow, error) {

	row, err := repository.GetShoppingTaskItemForUpdate(ctx, tx, req.ErrandTaskId, req.ErrandTaskItemId, captainID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("shopping task item not found"))
		}
		log.Error().
			Err(err).
			Int64("captain_id", captainID).
			Int64("errand_task_id", req.ErrandTaskId).
			Int64("errand_task_item_id", req.ErrandTaskItemId).
			Msg("failed to load shopping task item for update")
		return nil, newErrandInternalError("")
	}
	return row, nil
}

func updateTaskItem(
	ctx context.Context,
	tx bun.Tx,
	req *errandv1.SaveShoppingTaskItemRequest,
	expectedUpdatedAt time.Time,
	captainID int64,
) error {
	nonPurchaseReason := ""
	if req.NonPurchaseReason != nil {
		nonPurchaseReason = req.GetNonPurchaseReason()
	}
	now := time.Now().UTC()
	//更新 purchased_quantity、non_purchase_reason、handled_at、updated_at
	if err := repository.UpdateShoppingTaskItem(
		ctx,
		tx,
		req.ErrandTaskItemId,
		expectedUpdatedAt,
		req.PurchasedQuantity,
		nonPurchaseReason,
		now,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConcurrencyConflict
		}
		log.Error().
			Err(err).
			Int64("captain_id", captainID).
			Int64("errand_task_id", req.ErrandTaskId).
			Int64("errand_task_item_id", req.ErrandTaskItemId).
			Msg("failed to load shopping task item")
		return newErrandInternalError("")
	}
	return nil
}

// 将 errand_task.status 更新为 pending_distributing，并将关联 demand.status 同步到 pending_distributing
func TransitionToPendingDistributing(
	ctx context.Context,
	captainID int64,
	req *errandv1.TransitionToPendingDistributingRequest,
) error {
	if captainID <= 0 {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("missing captain id"))
	}
	if req == nil || req.ErrandTaskId <= 0 || req.UpdatedAt == nil || !req.UpdatedAt.IsValid() {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid transition"))
	}
	expectedUpdatedAt := req.UpdatedAt.AsTime().UTC()
	notificationRows, err := transitionTaskToPendingDistributing(ctx, captainID, req.ErrandTaskId, expectedUpdatedAt)
	if err != nil {
		return err
	}
	// 对 不采购和部分采购的购买人发送通知
	sendNonPurchasedNotifications(ctx, req.ErrandTaskId, notificationRows)
	return nil
}

// 在数据库事务中将指定的跑腿任务状态更新为“待分发”，并返回需要推送通知的未购买需求项列表。
func transitionTaskToPendingDistributing(
	ctx context.Context,
	captainID, taskID int64,
	expectedUpdatedAt time.Time,
) ([]repository.NonPurchasedDemandItemNotificationRow, error) {
	var notificationRows []repository.NonPurchasedDemandItemNotificationRow

	err := repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		rows, err := executeTransitionToPendingDistributingTx(ctx, tx, captainID, taskID, expectedUpdatedAt)
		if err != nil {
			return err
		}

		notificationRows = rows
		return nil
	})
	if err != nil {
		return nil, err
	}

	return notificationRows, nil
}

func executeTransitionToPendingDistributingTx(
	ctx context.Context,
	tx bun.Tx,
	captainID, taskID int64,
	expectedUpdatedAt time.Time,
) ([]repository.NonPurchasedDemandItemNotificationRow, error) {
	task, err := loadShoppingTaskForTransition(ctx, tx, captainID, taskID, expectedUpdatedAt)
	if err != nil {
		return nil, err
	}
	if err := ensureTaskItemsHandledForTransition(ctx, tx, taskID); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if err := updatePendingDistributingStatus(ctx, tx, task.TaskID, expectedUpdatedAt, now); err != nil {
		return nil, err
	}

	return loadPendingDistributingNotifications(ctx, tx, taskID)
}

// 加载任务并校验状态，乐观锁
func loadShoppingTaskForTransition(
	ctx context.Context,
	tx bun.Tx,
	captainID, taskID int64,
	expectedUpdatedAt time.Time,
) (*repository.ErrandTaskForUpdateRow, error) {
	task, err := repository.GetErrandTaskForUpdate(ctx, tx, taskID, captainID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("shopping task not found"))
		}
		log.Error().
			Err(err).
			Int64("captain_id", captainID).
			Int64("errand_task_id", taskID).
			Msg("failed to load errand task for update")
		return nil, newErrandInternalError("")
	}
	if task.Status != model.ErrandTaskStatusShopping {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("task is not in shopping status"))
	}
	if !task.UpdatedAt.UTC().Equal(expectedUpdatedAt) {
		return nil, ErrConcurrencyConflict
	}

	return task, nil
}

// 确保所有任务条目已处理（？
func ensureTaskItemsHandledForTransition(ctx context.Context, tx bun.Tx, taskID int64) error {
	summary, err := repository.GetTaskItemHandlingSummary(ctx, tx, taskID)
	if err != nil {
		log.Error().
			Err(err).
			Int64("errand_task_id", taskID).
			Msg("failed to load task item handling summary")
		return newErrandInternalError("")
	}
	if summary.TotalCount == 0 {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("task has no shopping items"))
	}
	if summary.UnhandledCount > 0 {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("unhandled shopping items"))
	}

	return nil
}

func updatePendingDistributingStatus(
	ctx context.Context,
	tx bun.Tx,
	taskID int64,
	expectedUpdatedAt, now time.Time,
) error {
	//更新demand主表状态
	if err := repository.UpdateTaskToPendingDistributing(ctx, tx, taskID, expectedUpdatedAt, now); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConcurrencyConflict
		}
		log.Error().
			Err(err).
			Int64("errand_task_id", taskID).
			Msg("failed to update task to pending distributing")
		return newErrandInternalError("")
	}
	//更新demandItem表状态
	if err := repository.UpdateTaskRelatedDemandsToPendingDistributing(ctx, tx, taskID, now); err != nil {
		log.Error().
			Err(err).
			Int64("errand_task_id", taskID).
			Msg("failed to update related demand items to pending distributing")
		return newErrandInternalError("")
	}

	return nil
}

func loadPendingDistributingNotifications(
	ctx context.Context,
	tx bun.Tx,
	taskID int64,
) ([]repository.NonPurchasedDemandItemNotificationRow, error) {
	rows, err := repository.ListNonPurchasedDemandItemNotifications(ctx, tx, taskID)
	if err != nil {
		log.Error().
			Err(err).
			Int64("errand_task_id", taskID).
			Msg("failed to load non-purchase notifications")
		return nil, newErrandInternalError("")
	}

	return rows, nil
}

func sendNonPurchasedNotifications(
	ctx context.Context,
	taskID int64,
	rows []repository.NonPurchasedDemandItemNotificationRow,
) {
	grouped := make(map[int64][]repository.NonPurchasedDemandItemNotificationRow)
	for _, row := range rows {
		grouped[row.TaskItemID] = append(grouped[row.TaskItemID], row) //按taskItemID分组，把统一任务下的demand聚合
	}

	for _, itemRows := range grouped {
		remaining := itemRows[0].PurchasedQuantity  // 假设团长买了5件，要分给所有有需求的人
		for _, row := range itemRows {
			purchasedForThisDemand := minInt32(remaining, row.RequiredQuantity)  // 如果剩余库存大于小王要的货量，要多少给多少，如果小于，有多少给多少
			remaining -= purchasedForThisDemand  // 减去小王拿走的货量
			// 完全满足需求或获取飞书账号失败
			if purchasedForThisDemand == row.RequiredQuantity || row.RequesterOpenID == "" {
				continue
			}

			statusText := "部分采购"
			if purchasedForThisDemand == 0 {
				statusText = "未采购"
			}
			text := fmt.Sprintf(
				"你的跑腿商品“%s”采购结果已更新：需求 %d 件，实际采购 %d 件，当前状态：%s。",
				row.TitleSnapshot,
				row.RequiredQuantity,
				purchasedForThisDemand,
				statusText,
			)
			// 业务幂等键，防止重复发送
			bizKey := fmt.Sprintf("errand:task:%d:demand-item:%d:shopping-result", taskID, row.DemandItemID)

			if _, err := feishu.SendTextByOpenID(ctx, row.RequesterOpenID, text, bizKey); err != nil {
				log.Warn().
					Err(err).
					Int64("task_id", taskID).
					Int64("demand_item_id", row.DemandItemID).
					Msg("failed to send errand shopping result notification")
			}
		}
	}
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
// 获取待分发和分发中的跑腿任务详情
func GetDistributingTaskDetail(
	ctx context.Context,
	captainID int64,
	req *errandv1.GetDistributingTaskDetailRequest,
) (*errandv1.GetDistributingTaskDetailResponse, error) {
	if captainID <= 0 {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing captain id"))
	}
	if req == nil || req.ErrandTaskId <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid errand task id"))
	}

	header, err := repository.GetDistributingTaskHeader(ctx, postgres.DB, req.ErrandTaskId, captainID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("distributing task not found"))
		}
		log.Error().
			Err(err).
			Int64("errand_task_id", req.ErrandTaskId).
			Int64("captain_id", captainID).
			Msg("failed to load distributing task header")
		return nil, newErrandInternalError("")
	}
	if header.Status != model.ErrandTaskStatusPendingDistributing &&
		header.Status != model.ErrandTaskStatusDistributing {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("task is not in distributing flow"))
	}

	rows, err := repository.ListDistributingTaskDetails(ctx, postgres.DB, header.TaskID)
	if err != nil {
		log.Error().Err(err).Int64("errand_task_id", header.TaskID).Msg("failed to load distributing task details")
		return nil, newErrandInternalError("")
	}
	// 数据聚合
	items := make([]*errandv1.DistributingItem, 0)
	itemByID := make(map[int64]*errandv1.DistributingItem, len(rows))
	for _, row := range rows {
		item := itemByID[row.TaskItemID]
		if item == nil {
			actual := int32(0)
			if row.ActualUnitPriceCents != nil {
				actual = *row.ActualUnitPriceCents
			}
			item = &errandv1.DistributingItem{
				ErrandTaskItemId:     row.TaskItemID,
				ProductTemplateId:    row.ProductTemplateID,
				TitleSnapshot:        row.TitleSnapshot,
				DescriptionSnapshot:  row.DescriptionSnapshot,
				ImageUrlSnapshot:     row.ImageURLSnapshot,
				OriginUnitPriceCents: row.OriginUnitPriceCents,
				ActualUnitPriceCents: actual,
				Requesters:           make([]*errandv1.DistributingRequestInfo, 0, 1),
			}
			itemByID[row.TaskItemID] = item
			items = append(items, item)
		}

		item.Requesters = append(item.Requesters, &errandv1.DistributingRequestInfo{
			PurchaserId:                   row.PurchaserID,
			PurchaserName:                 row.PurchaserName,
			PurchaserAvatarUrl:            row.PurchaserAvatarURL,
			Quantity:                      row.Quantity,
			DistributedQuantity:           row.DistributedQuantity,
			ErrandTaskAssignmentId:        row.TaskAssignmentID,
			ErrandDemandItemId:            row.DemandItemID,
			ErrandTaskAssignmentUpdatedAt: timestamppb.New(row.TaskAssignmentUpdatedAt),
		})
	}

	return &errandv1.GetDistributingTaskDetailResponse{
		ErrandTaskId:      header.TaskID,
		StoreId:           header.StoreID,
		StoreName:         header.StoreName,
		PackagingFeeCents: header.PackagingFeeCents,
		DistributingItems: items,
	}, nil
}
// 待分发阶段修改实际采购单价
func UpdateActualPrice(ctx context.Context, captainID int64, req *errandv1.UpdateActualPriceRequest) error {
	if captainID <= 0 {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("missing captain id"))
	}
	if req == nil || req.ErrandTaskId <= 0 || req.ErrandTaskItemId <= 0 ||
		req.ErrandTaskItemUpdatedAt == nil || !req.ErrandTaskItemUpdatedAt.IsValid() ||
		req.ActualUnitPriceCents < 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid update actual price request"))
	}

	expectedUpdatedAt := req.ErrandTaskItemUpdatedAt.AsTime().UTC()
	return updateActualPriceInTx(ctx, captainID, req, expectedUpdatedAt)
}

func updateActualPriceInTx(
	ctx context.Context,
	captainID int64,
	req *errandv1.UpdateActualPriceRequest,
	expectedUpdatedAt time.Time,
) error {
	return repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return executeUpdateActualPriceTx(ctx, tx, captainID, req, expectedUpdatedAt)
	})
}

func executeUpdateActualPriceTx(
	ctx context.Context,
	tx bun.Tx,
	captainID int64,
	req *errandv1.UpdateActualPriceRequest,
	expectedUpdatedAt time.Time,
) error {
	row, err := loadDistributingTaskItemForPriceUpdate(ctx, tx, captainID, req.ErrandTaskId, req.ErrandTaskItemId)
	if err != nil {
		return err
	}
	if err := validateActualPriceUpdate(row, expectedUpdatedAt, req.ActualUnitPriceCents); err != nil {
		return err
	}
	// 避免无意义的变更日志
	if actualPriceUnchanged(row, req.ActualUnitPriceCents) {
		return nil
	}
	if err := createActualPriceChangeLog(
		ctx,
		tx,
		captainID,
		req.ErrandTaskItemId,
		row.ActualUnitPriceCents,
		req.ActualUnitPriceCents,
	); err != nil {
		return err
	}

	return persistActualPrice(ctx, tx, captainID, req.ErrandTaskItemId, expectedUpdatedAt, req.ActualUnitPriceCents)
}
// 加载taskItem数据
func loadDistributingTaskItemForPriceUpdate(
	ctx context.Context,
	tx bun.Tx,
	captainID, taskID, taskItemID int64,
) (*repository.DistributingTaskItemForUpdateRow, error) {
	row, err := repository.GetDistributingTaskItemForUpdate(ctx, tx, taskID, taskItemID, captainID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("distributing task item not found"))
		}
		log.Error().
			Err(err).
			Int64("errand_task_id", taskID).
			Int64("errand_task_item_id", taskItemID).
			Int64("captain_id", captainID).
			Msg("failed to load distributing task item for update")
		return nil, newErrandInternalError("")
	}

	return row, nil
}
// 检查taskItem在待分配状态，乐观锁并发，更新purchased_quantity后，未采购条目的价格必须为零（？
func validateActualPriceUpdate(
	row *repository.DistributingTaskItemForUpdateRow,
	expectedUpdatedAt time.Time,
	actualUnitPriceCents int32,
) error {
	if row.TaskStatus != model.ErrandTaskStatusPendingDistributing {
		return connect.NewError(
			connect.CodeFailedPrecondition,
			errors.New("task is not in pending distributing status"),
		)
	}
	if !row.TaskItemUpdatedAt.UTC().Equal(expectedUpdatedAt) {
		return ErrConcurrencyConflict
	}
	if row.PurchasedQuantity == nil {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("task item has not been handled"))
	}
	if *row.PurchasedQuantity == 0 && actualUnitPriceCents != 0 {
		return connect.NewError(
			connect.CodeInvalidArgument,
			errors.New("fully-unpurchased item must use 0 actual price"),
		)
	}

	return nil
}
// 幂等性检查价格是否变化，如果相同直接返回成功
func actualPriceUnchanged(row *repository.DistributingTaskItemForUpdateRow, actualUnitPriceCents int32) bool {
	return row.ActualUnitPriceCents != nil && *row.ActualUnitPriceCents == actualUnitPriceCents
}
// 创建变更日志
func createActualPriceChangeLog(
	ctx context.Context,
	tx bun.Tx,
	captainID, taskItemID int64,
	oldUnitPriceCents *int32,
	newUnitPriceCents int32,
) error {
	if err := repository.CreatePriceChangeLog(ctx, tx, &model.ErrandPriceChangeLog{
		TaskItemID:        taskItemID,
		OperatorID:        captainID,
		OldUnitPriceCents: oldUnitPriceCents,
		NewUnitPriceCents: newUnitPriceCents,
	}); err != nil {
		log.Error().
			Err(err).
			Int64("errand_task_item_id", taskItemID).
			Int64("captain_id", captainID).
			Msg("failed to create price change log")
		return newErrandInternalError("")
	}

	return nil
}
// 持久化更新
func persistActualPrice(
	ctx context.Context,
	tx bun.Tx,
	captainID, taskItemID int64,
	expectedUpdatedAt time.Time,
	actualUnitPriceCents int32,
) error {
	now := time.Now().UTC()
	if err := repository.UpdateTaskItemActualPrice(
		ctx,
		tx,
		taskItemID,
		expectedUpdatedAt,
		actualUnitPriceCents,
		now,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConcurrencyConflict
		}
		log.Error().
			Err(err).
			Int64("errand_task_item_id", taskItemID).
			Int64("captain_id", captainID).
			Msg("failed to update actual price")
		return newErrandInternalError("")
	}

	return nil
}
// 将采购任务从待分发流转到分发中
func TransitionToDistributing(
	ctx context.Context,
	captainID int64,
	req *errandv1.TransitionToDistributingRequest,
) error {
	if captainID <= 0 {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("missing captain id"))
	}
	if req == nil || req.ErrandTaskId <= 0 || req.PackagingFeeCents < 0 ||
		req.UpdatedAt == nil || !req.UpdatedAt.IsValid() {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid transition to distributing request"))
	}

	expectedUpdatedAt := req.UpdatedAt.AsTime().UTC()
	return repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		task, err := loadPendingDistributingTaskForTransition(
			ctx,
			tx,
			captainID,
			req.ErrandTaskId,
			expectedUpdatedAt,
		)
		if err != nil {
			return err
		}

		return updateDistributingStatus(ctx, tx, task.TaskID, expectedUpdatedAt, req.PackagingFeeCents)
	})
}
// 加载数据，加锁
func loadPendingDistributingTaskForTransition(
	ctx context.Context,
	tx bun.Tx,
	captainID, taskID int64,
	expectedUpdatedAt time.Time,
) (*repository.ErrandTaskForUpdateRow, error) {
	task, err := repository.GetErrandTaskForUpdate(ctx, tx, taskID, captainID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("pending distributing task not found"))
		}
		log.Error().
			Err(err).
			Int64("captain_id", captainID).
			Int64("errand_task_id", taskID).
			Msg("failed to load pending distributing task for update")
		return nil, newErrandInternalError("")
	}
	if task.Status != model.ErrandTaskStatusPendingDistributing {
		return nil, connect.NewError(
			connect.CodeFailedPrecondition,
			errors.New("task is not in pending distributing status"),
		)
	}
	if !task.UpdatedAt.UTC().Equal(expectedUpdatedAt) {
		return nil, ErrConcurrencyConflict
	}

	return task, nil
}
//更新 errand_task.packaging_fee_cents，并将 task 与关联 demand 状态同步为 distributing
func updateDistributingStatus(
	ctx context.Context,
	tx bun.Tx,
	taskID int64,
	expectedUpdatedAt time.Time,
	packagingFeeCents int32,
) error {
	now := time.Now().UTC()
	if err := repository.UpdateTaskToDistributing(
		ctx,
		tx,
		taskID,
		expectedUpdatedAt,
		packagingFeeCents,
		now,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConcurrencyConflict
		}
		log.Error().
			Err(err).
			Int64("errand_task_id", taskID).
			Msg("failed to update task to distributing")
		return newErrandInternalError("")
	}
	if err := repository.UpdateTaskRelatedDemandsToDistributing(ctx, tx, taskID, now); err != nil {
		log.Error().
			Err(err).
			Int64("errand_task_id", taskID).
			Msg("failed to update related demands to distributing")
		return newErrandInternalError("")
	}
	if err := repository.UpdateTaskRelatedDemandItemsToDistributing(ctx, tx, taskID, now); err != nil {
		log.Error().
			Err(err).
			Int64("errand_task_id", taskID).
			Msg("failed to update related demand items to distributing")
		return newErrandInternalError("")
	}

	return nil
}
// 更新分发中的购买人分发结果（Phase 5 — 分发中）
func SaveDistributingTaskAssignment(
	ctx context.Context,
	captainID int64,
	req *errandv1.SaveDistributingTaskAssignmentRequest,
) error {
	if captainID <= 0 {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("missing captain id"))
	}
	if req == nil || req.ErrandTaskItemId <= 0 || req.ErrandTaskAssignmentId <= 0 ||
		req.DistributedQuantity < 0 ||
		req.ErrandTaskAssignmentUpdatedAt == nil || !req.ErrandTaskAssignmentUpdatedAt.IsValid() {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid distributing task assignment request"))
	}

	expectedUpdatedAt := req.ErrandTaskAssignmentUpdatedAt.AsTime().UTC()
	return repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return executeSaveDistributingTaskAssignmentTx(ctx, tx, captainID, req, expectedUpdatedAt)
	})
}

func executeSaveDistributingTaskAssignmentTx(
	ctx context.Context,
	tx bun.Tx,
	captainID int64,
	req *errandv1.SaveDistributingTaskAssignmentRequest,
	expectedUpdatedAt time.Time,
) error {
	row, err := loadDistributingTaskAssignmentForUpdate(
		ctx,
		tx,
		captainID,
		req.ErrandTaskItemId,
		req.ErrandTaskAssignmentId,
	)
	if err != nil {
		return err
	}
	if err := validateDistributingTaskAssignmentUpdate(ctx, tx, row, req.DistributedQuantity, expectedUpdatedAt); err != nil {
		return err
	}
	// 幂等性检查
	if row.DistributedQuantity == req.DistributedQuantity {
		return nil
	}
	// 存数据库
	return persistDistributingTaskAssignment(
		ctx,
		tx,
		captainID,
		req.ErrandTaskAssignmentId,
		expectedUpdatedAt,
		req.DistributedQuantity,
	)
}
// 加锁查询：基于 errand_task_assignment_updated_at 校验并发后更新实际分发数量
func loadDistributingTaskAssignmentForUpdate(
	ctx context.Context,
	tx bun.Tx,
	captainID, taskItemID, assignmentID int64,
) (*repository.DistributingTaskAssignmentForUpdateRow, error) {
	row, err := repository.GetDistributingTaskAssignmentForUpdate(ctx, tx, taskItemID, assignmentID, captainID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("distributing task assignment not found"))
		}
		log.Error().
			Err(err).
			Int64("captain_id", captainID).
			Int64("errand_task_item_id", taskItemID).
			Int64("errand_task_assignment_id", assignmentID).
			Msg("failed to load distributing task assignment for update")
		return nil, newErrandInternalError("")
	}

	return row, nil
}
// 校验 分配中 任务状态，乐观锁并发控制，purchased_quantity 已设置（？，分配数量不能超过需求量，总分配数量不能超过总采购量
func validateDistributingTaskAssignmentUpdate(
	ctx context.Context,
	tx bun.Tx,
	row *repository.DistributingTaskAssignmentForUpdateRow,
	distributedQuantity int32,
	expectedUpdatedAt time.Time,
) error {
	if row.TaskStatus != model.ErrandTaskStatusDistributing {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("task is not in distributing status"))
	}
	if !row.AssignmentUpdatedAt.UTC().Equal(expectedUpdatedAt) {
		return ErrConcurrencyConflict
	}
	if row.PurchasedQuantity == nil {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("task item has not been purchased"))
	}
	if distributedQuantity > row.DemandQuantity {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("distributed quantity exceeds demand quantity"))
	}
	// 计算当前task_item已经分发的总数
	totalDistributed, err := repository.SumTaskItemDistributedQuantity(ctx, tx, row.TaskItemID)
	if err != nil {
		log.Error().
			Err(err).
			Int64("errand_task_item_id", row.TaskItemID).
			Msg("failed to sum distributed quantity")
		return newErrandInternalError("")
	}
	// 计算更新后总数
	totalAfterUpdate := totalDistributed - int64(row.DistributedQuantity) + int64(distributedQuantity)
	// 如果更新后总数小于等于采购的数量
	// 小张想要 6 件
	//totalAfterUpdate = 5 - 0 + 6 = 11
	// 11 > 10  不允许（超过采购总量）
	if totalAfterUpdate > int64(*row.PurchasedQuantity) {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("distributed quantity exceeds purchased quantity"))
	}

	return nil
}

func persistDistributingTaskAssignment(
	ctx context.Context,
	tx bun.Tx,
	captainID, assignmentID int64,
	expectedUpdatedAt time.Time,
	distributedQuantity int32,
) error {
	now := time.Now().UTC()
	if err := repository.UpdateDistributingTaskAssignment(
		ctx,
		tx,
		assignmentID,
		expectedUpdatedAt,
		distributedQuantity,
		now,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConcurrencyConflict
		}
		log.Error().
			Err(err).
			Int64("captain_id", captainID).
			Int64("errand_task_assignment_id", assignmentID).
			Msg("failed to update distributing task assignment")
		return newErrandInternalError("")
	}

	return nil
}
// 分发完成后流转到收款中，并创建支付账单
func TransitionToCollectingPayment(
	ctx context.Context,
	captainID int64,
	req *errandv1.TransitionToCollectingPaymentRequest,
) error {
	if captainID <= 0 {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("missing captain id"))
	}
	if req == nil || req.ErrandTaskId <= 0 || req.UpdatedAt == nil || !req.UpdatedAt.IsValid() {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid transition to collecting payment request"))
	}

	expectedUpdatedAt := req.UpdatedAt.AsTime().UTC()
	if err := repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		task, err := loadDistributingTaskForCollectingPayment(
			ctx,
			tx,
			captainID,
			req.ErrandTaskId,
			expectedUpdatedAt,
		)
		if err != nil {
			return err
		}
		if err := ensureTaskDistributionCompleted(ctx, tx, task.TaskID); err != nil {
			return err
		}

		now := time.Now().UTC()
		if err := repository.UpdateTaskToCollectingPayment(
			ctx,
			tx,
			task.TaskID,
			expectedUpdatedAt,
			now,
		); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConcurrencyConflict
			}
			log.Error().
				Err(err).
				Int64("errand_task_id", task.TaskID).
				Msg("failed to update task to collecting payment")
			return newErrandInternalError("")
		}
		if err := repository.UpdateTaskRelatedDemandsToPendingPayment(ctx, tx, task.TaskID, now); err != nil {
			log.Error().
				Err(err).
				Int64("errand_task_id", task.TaskID).
				Msg("failed to update related demands to pending payment")
			return newErrandInternalError("")
		}
		if err := repository.UpdateTaskRelatedDemandItemsToPendingPayment(ctx, tx, task.TaskID, now); err != nil {
			log.Error().
				Err(err).
				Int64("errand_task_id", task.TaskID).
				Msg("failed to update related demand items to pending payment")
			return newErrandInternalError("")
		}

		return nil
	}); err != nil {
		return err
	}

	return createPaymentBillsForTask(ctx, req.ErrandTaskId)
}

func loadDistributingTaskForCollectingPayment(
	ctx context.Context,
	tx bun.Tx,
	captainID, taskID int64,
	expectedUpdatedAt time.Time,
) (*repository.ErrandTaskForUpdateRow, error) {
	task, err := repository.GetErrandTaskForUpdate(ctx, tx, taskID, captainID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("distributing task not found"))
		}
		log.Error().
			Err(err).
			Int64("captain_id", captainID).
			Int64("errand_task_id", taskID).
			Msg("failed to load distributing task for collecting payment")
		return nil, newErrandInternalError("")
	}
	if task.Status != model.ErrandTaskStatusDistributing {
		return nil, connect.NewError(
			connect.CodeFailedPrecondition,
			errors.New("task is not in distributing status"),
		)
	}
	if !task.UpdatedAt.UTC().Equal(expectedUpdatedAt) {
		return nil, ErrConcurrencyConflict
	}

	return task, nil
}
// 校验分配状态，确保分配已完成
func ensureTaskDistributionCompleted(ctx context.Context, tx bun.Tx, taskID int64) error {
	summary, err := repository.GetTaskDistributionSummary(ctx, tx, taskID)
	if err != nil {
		log.Error().
			Err(err).
			Int64("errand_task_id", taskID).
			Msg("failed to load task distribution summary")
		return newErrandInternalError("")
	}
	if summary.TotalTaskItemCount == 0 {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("task has no distributing items"))
	}
	if summary.UnhandledCount > 0 {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("unhandled shopping items"))
	}
	if summary.UnpricedCount > 0 {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("unpriced distributing items"))
	}
	if summary.IncompleteCount > 0 {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("incomplete distributing items"))
	}

	return nil
}

func OnPaymentConfirmed(ctx context.Context, req *errandv1.OnPaymentConfirmedRequest) error {
	if req == nil || req.SourceType != "errand_task" || req.SourceId <= 0 || req.PayerId <= 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid payment confirmed request"))
	}

	now := time.Now().UTC()
	if err := repository.UpdateTaskDemandItemsToCompletedByPayer(
		ctx,
		postgres.DB,
		req.SourceId,
		req.PayerId,
		now,
	); err != nil {
		log.Error().
			Err(err).
			Int64("errand_task_id", req.SourceId).
			Int64("payer_id", req.PayerId).
			Msg("failed to update payer demand items to completed")
		return newErrandInternalError("")
	}

	return nil
}

func OnAllPaymentsConfirmed(ctx context.Context, req *errandv1.OnAllPaymentsConfirmedRequest) error {
	if req == nil || req.SourceType != "errand_task" || req.SourceId <= 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid all payments confirmed request"))
	}

	return repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		task, err := repository.GetErrandTaskForUpdateByID(ctx, tx, req.SourceId)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return connect.NewError(connect.CodeNotFound, errors.New("collecting payment task not found"))
			}
			log.Error().
				Err(err).
				Int64("errand_task_id", req.SourceId).
				Msg("failed to load task for all payments confirmed")
			return newErrandInternalError("")
		}
		if task.Status == model.ErrandTaskStatusCompleted {
			return nil
		}
		if task.Status != model.ErrandTaskStatusCollectingPayment {
			return connect.NewError(
				connect.CodeFailedPrecondition,
				errors.New("task is not in collecting payment status"),
			)
		}
		if err := ensureTaskPaymentsCompleted(ctx, tx, task.TaskID); err != nil {
			return err
		}

		now := time.Now().UTC()
		if err := repository.UpdateTaskToCompletedWithoutUpdatedAt(ctx, tx, task.TaskID, now); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConcurrencyConflict
			}
			log.Error().
				Err(err).
				Int64("errand_task_id", task.TaskID).
				Msg("failed to update task to completed after all payments confirmed")
			return newErrandInternalError("")
		}
		if err := repository.UpdateTaskRelatedDemandsToCompleted(ctx, tx, task.TaskID, now); err != nil {
			log.Error().
				Err(err).
				Int64("errand_task_id", task.TaskID).
				Msg("failed to update related demands to completed after all payments confirmed")
			return newErrandInternalError("")
		}
		if err := repository.UpdateTaskRelatedDemandItemsToCompleted(ctx, tx, task.TaskID, now); err != nil {
			log.Error().
				Err(err).
				Int64("errand_task_id", task.TaskID).
				Msg("failed to update related demand items to completed after all payments confirmed")
			return newErrandInternalError("")
		}

		return nil
	})
}

type taskPaymentBillDraft struct {
	PayerID     int64
	PayeeID     int64
	AmountCents int32
}
// 生成支付订单，放在事务外，订单创建失败不影响状态转换
func createPaymentBillsForTask(ctx context.Context, taskID int64) error {
	// 检查支付服务客户端
	if client.PaymentInternalServiceClient == nil {
		log.Error().Int64("errand_task_id", taskID).Msg("payment internal service client is not initialized")
		return newErrandInternalError("")
	}
	// 分配查询数据
	rows, err := repository.ListTaskPaymentBillAssignments(ctx, postgres.DB, taskID)
	if err != nil {
		log.Error().
			Err(err).
			Int64("errand_task_id", taskID).
			Msg("failed to load errand task payment bill assignments")
		return newErrandInternalError("")
	}
	if len(rows) == 0 {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("task has no payment assignments"))
	}
	// 构建账单草稿
	for _, draft := range buildTaskPaymentBillDrafts(rows) {
		// 调用支付服务创建账单
		resp, err := client.PaymentInternalServiceClient.CreateBillForOrder(
			ctx,
			connect.NewRequest(&paymentv1.CreateBillForOrderRequest{
				SourceType:  "errand_task",
				SourceId:    taskID,
				PayerId:     draft.PayerID,
				PayeeId:     draft.PayeeID,
				AmountCents: draft.AmountCents,
			}),
		)
		if err != nil {
			log.Error().
				Err(err).
				Int64("errand_task_id", taskID).
				Int64("payer_id", draft.PayerID).
				Msg("failed to create errand task payment bill")
			return newErrandInternalError("")
		}
		if resp.Msg == nil || resp.Msg.Bill == nil || resp.Msg.Bill.Id <= 0 {
			log.Error().
				Int64("errand_task_id", taskID).
				Int64("payer_id", draft.PayerID).
				Msg("payment service returned empty bill")
			return newErrandInternalError("")
		}

		if err := repository.UpdateTaskAssignmentPaymentBillIDByPayer(
			ctx,
			postgres.DB,
			taskID,
			draft.PayerID,
			resp.Msg.Bill.Id,
			time.Now().UTC(),
		); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return newErrandInternalError("payment assignment missing")
			}
			log.Error().
				Err(err).
				Int64("errand_task_id", taskID).
				Int64("payer_id", draft.PayerID).
				Int64("payment_bill_id", resp.Msg.Bill.Id).
				Msg("failed to bind payment bill to errand task assignments")
			return newErrandInternalError("")
		}
	}

	return nil
}
// 将多个分配记录聚合为每个用户的账单草稿
func buildTaskPaymentBillDrafts(rows []repository.TaskPaymentBillAssignmentRow) []taskPaymentBillDraft {
	type billGroup struct {
		payerID int64
		payeeID int64
		amount  int64
	}

	groups := make(map[int64]*billGroup)
	payerIDs := make([]int64, 0)
	var packagingFeeCents int32 // 总包装费
	for i, row := range rows {
		if i == 0 {
			packagingFeeCents = row.PackagingFeeCents
		}
		// 按付款人分组
		group, ok := groups[row.PayerID] 
		if !ok {
			group = &billGroup{
				payerID: row.PayerID,
				payeeID: row.PayeeID,
			}
			groups[row.PayerID] = group
			payerIDs = append(payerIDs, row.PayerID)   // 记录新付款人
		}

		productAmount := int64(row.ActualUnitPriceCents) * int64(row.DistributedQuantity)
		serviceFeeAmount := int64(row.ServiceFeePerUnitCents) * int64(row.DistributedQuantity)
		group.amount += productAmount + serviceFeeAmount
	}
	// 计算包装费分摊（向上取整除法）
	packagingShare := int64(ceilDivide(packagingFeeCents, int32(len(payerIDs))))
	// 生成草稿账单
	drafts := make([]taskPaymentBillDraft, 0, len(payerIDs))
	for _, payerID := range payerIDs {
		group := groups[payerID]
		drafts = append(drafts, taskPaymentBillDraft{
			PayerID:     group.payerID,
			PayeeID:     group.payeeID,
			AmountCents: int32(group.amount + packagingShare),
		})
	}

	return drafts
}

// 获取收款中状态的跑腿任务详情
func GetCollectingPaymentDetail(
	ctx context.Context,
	captainID int64,
	req *errandv1.GetCollectingPaymentDetailRequest,
) (*errandv1.GetCollectingPaymentDetailResponse, error) {
	if captainID <= 0 {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing captain id"))
	}
	if req == nil || req.ErrandTaskId <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid errand task id"))
	}

	header, err := repository.GetCollectingPaymentTaskHeader(ctx, postgres.DB, req.ErrandTaskId, captainID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("collecting payment task not found"))
		}
		log.Error().
			Err(err).
			Int64("errand_task_id", req.ErrandTaskId).
			Int64("captain_id", captainID).
			Msg("failed to load collecting payment task header")
		return nil, newErrandInternalError("")
	}
	if header.Status != model.ErrandTaskStatusCollectingPayment {
		return nil, connect.NewError(
			connect.CodeFailedPrecondition,
			errors.New("task is not in collecting payment status"),
		)
	}

	rows, err := repository.ListCollectingPaymentDetails(ctx, postgres.DB, header.TaskID)
	if err != nil {
		log.Error().
			Err(err).
			Int64("errand_task_id", header.TaskID).
			Msg("failed to load collecting payment details")
		return nil, newErrandInternalError("")
	}

	return &errandv1.GetCollectingPaymentDetailResponse{
		Bills: buildCollectingPaymentBills(header, rows),
	}, nil
}

func buildCollectingPaymentBills(
	header *repository.CollectingPaymentTaskHeaderRow,
	rows []repository.CollectingPaymentDetailRow,
) []*errandv1.CollectingPaymentBillDetail {
	type billGroup struct {
		rows    []repository.CollectingPaymentDetailRow
		billRow *repository.CollectingPaymentDetailRow
	}

	groups := make(map[int64]*billGroup)
	payerIDs := make([]int64, 0)
	for i := range rows {
		row := &rows[i]
		group, ok := groups[row.PayerID]
		if !ok {
			group = &billGroup{}
			groups[row.PayerID] = group
			payerIDs = append(payerIDs, row.PayerID)
		}
		group.rows = append(group.rows, *row)
		if group.billRow == nil && row.PaymentBillID != nil {
			group.billRow = row
		}
	}

	packagingShare := ceilDivide(header.PackagingFeeCents, int32(len(payerIDs)))
	bills := make([]*errandv1.CollectingPaymentBillDetail, 0, len(payerIDs))
	for _, payerID := range payerIDs {
		group := groups[payerID]
		items := make([]*errandv1.CollectingPaymentRequesterItemDetail, 0, len(group.rows))
		var productAmountCents int64
		var serviceFeeAmountCents int64
		var packagingFeeShareCents int64

		for i, row := range group.rows {
			productAmount := int64(row.ActualUnitPriceCents) * int64(row.DistributedQuantity)
			serviceFeeAmount := int64(row.ServiceFeePerUnitCents) * int64(row.DistributedQuantity)
			itemPackagingShare := int64(0)
			if i == 0 {
				itemPackagingShare = int64(packagingShare)
			}

			productAmountCents += productAmount
			serviceFeeAmountCents += serviceFeeAmount
			packagingFeeShareCents += itemPackagingShare

			item := &errandv1.CollectingPaymentRequesterItemDetail{
				ErrandDemandItemId:     row.DemandItemID,
				TitleSnapshot:          row.TitleSnapshot,
				RequiredQuantity:       row.RequiredQuantity,
				PurchasedQuantity:      row.PurchasedQuantity,
				DistributedQuantity:    row.DistributedQuantity,
				ActualUnitPriceCents:   row.ActualUnitPriceCents,
				ProductAmountCents:     int32(productAmount),
				ServiceFeePerUnitCents: row.ServiceFeePerUnitCents,
				ServiceFeeAmountCents:  int32(serviceFeeAmount),
				PackagingFeeShareCents: int32(itemPackagingShare),
				SubtotalCents:          int32(productAmount + serviceFeeAmount + itemPackagingShare),
			}
			if row.NonPurchaseReason != "" {
				reason := row.NonPurchaseReason
				item.NonPurchaseReason = &reason
			}
			items = append(items, item)
		}

		billDetail := &errandv1.CollectingPaymentBillDetail{
			RequesterId:            payerID,
			RequesterName:          group.rows[0].PayerName,
			RequesterAvatarUrl:     group.rows[0].PayerAvatarURL,
			PaymentStatus:          paymentv1.BillStatus_BILL_STATUS_UNSPECIFIED,
			Items:                  items,
			ProductAmountCents:     int32(productAmountCents),
			ServiceFeeAmountCents:  int32(serviceFeeAmountCents),
			PackagingFeeShareCents: int32(packagingFeeShareCents),
			TotalAmountCents:       int32(productAmountCents + serviceFeeAmountCents + packagingFeeShareCents),
		}
		if group.billRow != nil {
			billDetail.PaymentStatus = collectingPaymentBillStatusToProto(group.billRow.BillStatus)
			billDetail.Bill = collectingPaymentBillToProto(group.billRow)
		}
		bills = append(bills, billDetail)
	}

	return bills
}

func ceilDivide(value, divisor int32) int32 {
	if value <= 0 || divisor <= 0 {
		return 0
	}
	return (value + divisor - 1) / divisor
}

func collectingPaymentBillStatusToProto(status string) paymentv1.BillStatus {
	switch status {
	case "unpaid":
		return paymentv1.BillStatus_BILL_STATUS_UNPAID
	case "submitted":
		return paymentv1.BillStatus_BILL_STATUS_SUBMITTED
	case "completed":
		return paymentv1.BillStatus_BILL_STATUS_COMPLETED
	case "closed":
		return paymentv1.BillStatus_BILL_STATUS_CLOSED
	default:
		return paymentv1.BillStatus_BILL_STATUS_UNSPECIFIED
	}
}

func collectingPaymentChannelToProto(channel string) paymentv1.Channel {
	switch channel {
	case "wechat":
		return paymentv1.Channel_CHANNEL_WECHAT
	case "alipay":
		return paymentv1.Channel_CHANNEL_ALIPAY
	default:
		return paymentv1.Channel_CHANNEL_UNSPECIFIED
	}
}

func collectingPaymentBillToProto(row *repository.CollectingPaymentDetailRow) *paymentv1.Bill {
	if row == nil || row.PaymentBillID == nil {
		return nil
	}

	bill := &paymentv1.Bill{
		Id:     *row.PaymentBillID,
		BillNo: row.BillNo,
		Payer: &userv1.UserInfo{
			Id:        row.PayerID,
			Name:      row.PayerName,
			AvatarUrl: row.PayerAvatarURL,
		},
		Payee: &userv1.UserInfo{
			Id:        row.PayeeID,
			Name:      row.PayeeName,
			AvatarUrl: row.PayeeAvatarURL,
		},
		Status:     collectingPaymentBillStatusToProto(row.BillStatus),
		VerifyCode: row.VerifyCode,
		Channel:    collectingPaymentChannelToProto(stringValue(row.PaymentChannel)),
		CreatedAt:  timestamppb.New(timeValue(row.BillCreatedAt)),
		UpdatedAt:  timestamppb.New(timeValue(row.BillUpdatedAt)),
	}
	if row.BillAmountCents != nil {
		bill.AmountCents = *row.BillAmountCents
	}
	if row.SerialNumber != nil && *row.SerialNumber != "" {
		bill.SerialNumber = row.SerialNumber
	}
	if row.SubmittedAt != nil {
		bill.SubmittedAt = timestamppb.New(*row.SubmittedAt)
	}
	if row.CompletedAt != nil {
		bill.CompletedAt = timestamppb.New(*row.CompletedAt)
	}
	if row.ClosedAt != nil {
		bill.ClosedAt = timestamppb.New(*row.ClosedAt)
	}
	if row.SourceType != nil {
		bill.SourceType = row.SourceType
	}
	if row.SourceID != nil {
		bill.SourceId = row.SourceID
	}

	return bill
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func timeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
// 所有账单确认后将采购任务标记为完成
func TransitionToCompleted(
	ctx context.Context,
	captainID int64,
	req *errandv1.TransitionToCompletedRequest,
) error {
	if captainID <= 0 {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("missing captain id"))
	}
	if req == nil || req.ErrandTaskId <= 0 || req.UpdatedAt == nil || !req.UpdatedAt.IsValid() {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid transition to completed request"))
	}

	expectedUpdatedAt := req.UpdatedAt.AsTime().UTC()
	return repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		task, err := loadCollectingPaymentTaskForCompletion(ctx, tx, captainID, req.ErrandTaskId, expectedUpdatedAt)
		if err != nil {
			return err
		}
		if err := ensureTaskPaymentsCompleted(ctx, tx, task.TaskID); err != nil {
			return err
		}

		now := time.Now().UTC()
		if err := repository.UpdateTaskToCompleted(ctx, tx, task.TaskID, expectedUpdatedAt, now); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConcurrencyConflict
			}
			log.Error().
				Err(err).
				Int64("errand_task_id", task.TaskID).
				Msg("failed to update task to completed")
			return newErrandInternalError("")
		}
		if err := repository.UpdateTaskRelatedDemandsToCompleted(ctx, tx, task.TaskID, now); err != nil {
			log.Error().
				Err(err).
				Int64("errand_task_id", task.TaskID).
				Msg("failed to update related demands to completed")
			return newErrandInternalError("")
		}
		if err := repository.UpdateTaskRelatedDemandItemsToCompleted(ctx, tx, task.TaskID, now); err != nil {
			log.Error().
				Err(err).
				Int64("errand_task_id", task.TaskID).
				Msg("failed to update related demand items to completed")
			return newErrandInternalError("")
		}

		return nil
	})
}

func loadCollectingPaymentTaskForCompletion(
	ctx context.Context,
	tx bun.Tx,
	captainID, taskID int64,
	expectedUpdatedAt time.Time,
) (*repository.ErrandTaskForUpdateRow, error) {
	task, err := repository.GetErrandTaskForUpdate(ctx, tx, taskID, captainID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("collecting payment task not found"))
		}
		log.Error().
			Err(err).
			Int64("captain_id", captainID).
			Int64("errand_task_id", taskID).
			Msg("failed to load collecting payment task for completion")
		return nil, newErrandInternalError("")
	}
	if task.Status != model.ErrandTaskStatusCollectingPayment {
		return nil, connect.NewError(
			connect.CodeFailedPrecondition,
			errors.New("task is not in collecting payment status"),
		)
	}
	if !task.UpdatedAt.UTC().Equal(expectedUpdatedAt) {
		return nil, ErrConcurrencyConflict
	}

	return task, nil
}

func ensureTaskPaymentsCompleted(ctx context.Context, tx bun.Tx, taskID int64) error {
	summary, err := repository.GetTaskPaymentSummary(ctx, tx, taskID)
	if err != nil {
		log.Error().
			Err(err).
			Int64("errand_task_id", taskID).
			Msg("failed to load task payment summary")
		return newErrandInternalError("")
	}
	if summary.PayerCount == 0 {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("task has no payment payer"))
	}
	if summary.IncompleteBillCount > 0 {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("task payments are not completed"))
	}

	return nil
}

const (
	defaultErrandTaskListPageSize = int32(20)
	maxErrandTaskListPageSize     = int32(100)
)
// 获取当前团长的跑腿任务列表
func GetErrandTaskList(
	ctx context.Context,
	captainID int64,
	req *errandv1.GetErrandTaskListRequest,
) (*errandv1.GetErrandTaskListResponse, error) {
	if captainID <= 0 {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing captain id"))
	}
	if req == nil {
		req = &errandv1.GetErrandTaskListRequest{}
	}

	page, pageSize := normalizeErrandTaskListPage(req.Page, req.PageSize)
	statusFilter, err := buildErrandTaskStatusFilter(req.FilterStatus)
	if err != nil {
		return nil, err
	}

	totalCount, err := repository.CountErrandTasks(ctx, postgres.DB, captainID, statusFilter)
	if err != nil {
		log.Error().
			Err(err).
			Int64("captain_id", captainID).
			Msg("failed to count errand tasks")
		return nil, newErrandInternalError("")
	}

	rows, err := repository.ListErrandTasks(
		ctx,
		postgres.DB,
		captainID,
		statusFilter,
		int(pageSize),
		int((page-1)*pageSize),
	)
	if err != nil {
		log.Error().
			Err(err).
			Int64("captain_id", captainID).
			Msg("failed to list errand tasks")
		return nil, newErrandInternalError("")
	}

	tasks, taskIDs := errandTaskListRowsToProto(rows)
	itemRows, err := repository.ListErrandTaskItems(ctx, postgres.DB, taskIDs)
	if err != nil {
		log.Error().
			Err(err).
			Int64("captain_id", captainID).
			Msg("failed to list errand task items")
		return nil, newErrandInternalError("")
	}
	appendErrandTaskItems(tasks, itemRows)

	return &errandv1.GetErrandTaskListResponse{
		ErrandTasks: tasks,
		CurrentPage: page,
		TotalCount:  totalCount,
	}, nil
}

func normalizeErrandTaskListPage(page, pageSize int32) (int32, int32) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = defaultErrandTaskListPageSize
	}
	if pageSize > maxErrandTaskListPageSize {
		pageSize = maxErrandTaskListPageSize
	}

	return page, pageSize
}

func buildErrandTaskStatusFilter(protoStatus *errandv1.ErrandTaskStatus) (*model.ErrandTaskStatus, error) {
	if protoStatus == nil || *protoStatus == errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_UNSPECIFIED {
		return nil, nil
	}

	status, ok := protoErrandTaskStatusToModel(*protoStatus)
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid errand task status"))
	}

	return &status, nil
}

func errandTaskListRowsToProto(rows []repository.ErrandTaskListRow) ([]*errandv1.ErrandTask, []int64) {
	tasks := make([]*errandv1.ErrandTask, 0, len(rows))
	taskIDs := make([]int64, 0, len(rows))

	for _, row := range rows {
		status, _ := modelErrandTaskStatusToProto(row.Status)
		tasks = append(tasks, &errandv1.ErrandTask{
			TaskId:    row.TaskID,
			StoreId:   row.StoreID,
			StoreName: row.StoreName,
			Status:    status,
			Items:     make([]*errandv1.ErrandTaskItem, 0),
			CreatedAt: timestamppb.New(row.CreatedAt),
		})
		taskIDs = append(taskIDs, row.TaskID)
	}

	return tasks, taskIDs
}

func appendErrandTaskItems(tasks []*errandv1.ErrandTask, rows []repository.ErrandTaskListItemRow) {
	taskByID := make(map[int64]*errandv1.ErrandTask, len(tasks))
	for _, task := range tasks {
		taskByID[task.TaskId] = task
	}

	for _, row := range rows {
		task := taskByID[row.TaskID]
		if task == nil {
			continue
		}
		task.Items = append(task.Items, errandTaskListItemRowToProto(row, task.StoreId))
	}
}

func errandTaskListItemRowToProto(row repository.ErrandTaskListItemRow, storeID int64) *errandv1.ErrandTaskItem {
	actualUnitPriceCents := int32(0)
	if row.ActualUnitPriceCents != nil {
		actualUnitPriceCents = *row.ActualUnitPriceCents
	}

	item := &errandv1.ErrandTaskItem{
		Id: row.TaskItemID,
		ProductSnapshot: &catalogv1.ProductTemplate{
			Id:           row.ProductTemplateID,
			Title:        row.TitleSnapshot,
			Description:  row.DescriptionSnapshot,
			PriceCents:   row.ProductPriceCents,
			StoreId:      storeID,
			MainImageUrl: row.ImageURLSnapshot,
		},
		RequiredQuantity:     row.RequiredQuantity,
		ActualUnitPriceCents: actualUnitPriceCents,
		UpdatedAt:            timestamppb.New(row.UpdatedAt),
	}
	if row.PurchasedQuantity != nil {
		purchasedQuantity := *row.PurchasedQuantity
		item.PurchasedQuantity = &purchasedQuantity
	}
	if row.NonPurchaseReason != "" {
		nonPurchaseReason := row.NonPurchaseReason
		item.NonPurchaseReason = &nonPurchaseReason
	}

	return item
}
// 取消未完成的跑腿任务
func CancelTask(ctx context.Context, captainID int64, req *errandv1.CancelTaskRequest) error {
	if captainID <= 0 {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("missing captain id"))
	}
	if req == nil || req.ErrandTaskId <= 0 || req.UpdatedAt == nil || !req.UpdatedAt.IsValid() {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid cancel task request"))
	}

	expectedUpdatedAt := req.UpdatedAt.AsTime().UTC()
	var cancelledFromStatus model.ErrandTaskStatus
	if err := repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		task, err := loadTaskForCancellation(ctx, tx, captainID, req.ErrandTaskId, expectedUpdatedAt)
		if err != nil {
			return err
		}
		cancelledFromStatus = task.Status

		now := time.Now().UTC()
		if err := repository.UpdateTaskToCancelled(ctx, tx, task.TaskID, expectedUpdatedAt, now); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConcurrencyConflict
			}
			log.Error().
				Err(err).
				Int64("errand_task_id", task.TaskID).
				Msg("failed to update task to cancelled")
			return newErrandInternalError("")
		}
		if err := repository.UpdateTaskRelatedDemandsToCancelled(ctx, tx, task.TaskID, now); err != nil {
			log.Error().
				Err(err).
				Int64("errand_task_id", task.TaskID).
				Msg("failed to update related demands to cancelled")
			return newErrandInternalError("")
		}
		if err := repository.UpdateTaskRelatedDemandItemsToCancelled(ctx, tx, task.TaskID, now); err != nil {
			log.Error().
				Err(err).
				Int64("errand_task_id", task.TaskID).
				Msg("failed to update related demand items to cancelled")
			return newErrandInternalError("")
		}

		return nil
	}); err != nil {
		return err
	}

	if cancelledFromStatus == model.ErrandTaskStatusCollectingPayment {
		return cancelTaskPaymentBills(ctx, req.ErrandTaskId)
	}

	return nil
}

func loadTaskForCancellation(
	ctx context.Context,
	tx bun.Tx,
	captainID, taskID int64,
	expectedUpdatedAt time.Time,
) (*repository.ErrandTaskForUpdateRow, error) {
	task, err := repository.GetErrandTaskForUpdate(ctx, tx, taskID, captainID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("errand task not found"))
		}
		log.Error().
			Err(err).
			Int64("captain_id", captainID).
			Int64("errand_task_id", taskID).
			Msg("failed to load task for cancellation")
		return nil, newErrandInternalError("")
	}
	if task.Status == model.ErrandTaskStatusCompleted || task.Status == model.ErrandTaskStatusCancelled {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("task cannot be cancelled"))
	}
	if !task.UpdatedAt.UTC().Equal(expectedUpdatedAt) {
		return nil, ErrConcurrencyConflict
	}

	return task, nil
}

func cancelTaskPaymentBills(ctx context.Context, taskID int64) error {
	if client.PaymentInternalServiceClient == nil {
		log.Error().Int64("errand_task_id", taskID).Msg("payment internal service client is not initialized")
		return newErrandInternalError("")
	}

	if _, err := client.PaymentInternalServiceClient.CancelBillBySource(
		ctx,
		connect.NewRequest(&paymentv1.CancelBillBySourceRequest{
			SourceType: "errand_task",
			SourceId:   taskID,
		}),
	); err != nil {
		log.Error().
			Err(err).
			Int64("errand_task_id", taskID).
			Msg("failed to cancel errand task payment bills")
		return newErrandInternalError("")
	}

	return nil
}

func protoErrandTaskStatusToModel(status errandv1.ErrandTaskStatus) (model.ErrandTaskStatus, bool) {
	switch status {
	case errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_SHOPPING:
		return model.ErrandTaskStatusShopping, true
	case errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_PENDING_DISTRIBUTING:
		return model.ErrandTaskStatusPendingDistributing, true
	case errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_DISTRIBUTING:
		return model.ErrandTaskStatusDistributing, true
	case errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_COLLECTING_PAYMENT:
		return model.ErrandTaskStatusCollectingPayment, true
	case errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_COMPLETED:
		return model.ErrandTaskStatusCompleted, true
	case errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_CANCELLED:
		return model.ErrandTaskStatusCancelled, true
	default:
		return "", false
	}
}

func modelErrandTaskStatusToProto(status model.ErrandTaskStatus) (errandv1.ErrandTaskStatus, bool) {
	switch status {
	case model.ErrandTaskStatusShopping:
		return errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_SHOPPING, true
	case model.ErrandTaskStatusPendingDistributing:
		return errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_PENDING_DISTRIBUTING, true
	case model.ErrandTaskStatusDistributing:
		return errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_DISTRIBUTING, true
	case model.ErrandTaskStatusCollectingPayment:
		return errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_COLLECTING_PAYMENT, true
	case model.ErrandTaskStatusCompleted:
		return errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_COMPLETED, true
	case model.ErrandTaskStatusCancelled:
		return errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_CANCELLED, true
	default:
		return errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_UNSPECIFIED, false
	}
}
