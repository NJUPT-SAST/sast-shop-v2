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
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/feishu"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
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
		// 根据店铺id和选项id加载数据
		loaded, err := loadCreateTaskData(ctx, tx, req.StoreId, selection)
		if err != nil {
			return err
		}
		// 创建任务，在errand_task表中插入新纪录，task进入shopping状态
		task, err := createShoppingTask(ctx, tx, captainID, req.StoreId)
		if err != nil {
			return err
		}
		taskID = task.ID
		// 按 product_template_id + deadline 聚合被选中的 demand_item，写入 errand_task_item
		taskItemIDByDemandItemID, err := createGroupedTaskItems(ctx, tx, task.ID, loaded.Rows, loaded.Snapshots)
		if err != nil {
			return err
		}
		// 创建任务分配，根据加载的数据在errand_task_assigniments表中插入数据
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
	// 悲观锁查询task和task_item
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
		// 校验前端请求更新时间和店铺id 与数据库是否一致
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

// 将选中的商品按商品id和截止时间分组
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
	grouped := buildTaskItemGroups(rows, snapshots)
	taskItemIDByDemandItemID := make(map[int64]int64, len(rows))

	for key, group := range grouped {
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
		if !row.TaskItemUpdatedAt.UTC().Equal(expectedUpdatedAt) {
			return ErrConcurrencyConflict
		}
		if req.PurchasedQuantity < 0 || req.PurchasedQuantity > row.RequiredQuantity {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid purchased quantity"))
		}

		return updateTaskItem(ctx, tx, req, expectedUpdatedAt, captainID)
	})
}

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

// 将采购任务从采购中流转到待分发
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

	sendNonPurchasedNotifications(ctx, req.ErrandTaskId, notificationRows)
	return nil
}

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
		grouped[row.TaskItemID] = append(grouped[row.TaskItemID], row)
	}

	for _, itemRows := range grouped {
		remaining := itemRows[0].PurchasedQuantity
		for _, row := range itemRows {
			purchasedForThisDemand := minInt32(remaining, row.RequiredQuantity)
			remaining -= purchasedForThisDemand
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

func actualPriceUnchanged(row *repository.DistributingTaskItemForUpdateRow, actualUnitPriceCents int32) bool {
	return row.ActualUnitPriceCents != nil && *row.ActualUnitPriceCents == actualUnitPriceCents
}

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
	if row.DistributedQuantity == req.DistributedQuantity {
		return nil
	}

	return persistDistributingTaskAssignment(
		ctx,
		tx,
		captainID,
		req.ErrandTaskAssignmentId,
		expectedUpdatedAt,
		req.DistributedQuantity,
	)
}

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

	totalDistributed, err := repository.SumTaskItemDistributedQuantity(ctx, tx, row.TaskItemID)
	if err != nil {
		log.Error().
			Err(err).
			Int64("errand_task_item_id", row.TaskItemID).
			Msg("failed to sum distributed quantity")
		return newErrandInternalError("")
	}
	totalAfterUpdate := totalDistributed - int64(row.DistributedQuantity) + int64(distributedQuantity)
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
