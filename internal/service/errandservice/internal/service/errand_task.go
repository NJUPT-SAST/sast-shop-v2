package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/idgen"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/client"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/repository"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
)

// 按截止时间和相同商品类型聚合
type taskItemGroupKey struct {
	ProductTemplateID int64
	Deadline          time.Time
}

type taskItemGroup struct {
	Snapshot         productSnapshot
	RequiredQuantity int32
	Rows             []repository.SelectedDemandItemRow
}

type productSnapshot struct {
	Title        string
	Description  string
	StoreID      int64
	MainImageURL string
}

type createTaskSelection struct {
	SelectedUpdatedAt map[int64]time.Time
	SelectedIDs       []int64
}

type createTaskLoadResult struct {
	Now        time.Time
	Rows       []repository.SelectedDemandItemRow
	DemandRows map[int64][]repository.SelectedDemandItemRow
	Snapshots  map[int64]productSnapshot
}

var (
	ErrInvalidDemandItem = errors.New("invalid demand item")

	ErrConcurrencyConflict = errors.New("concurrency conflict")
	ErrStoreMismatch       = errors.New("store mismatch")
	ErrDemandItemNotOpen   = errors.New("demand item not open")
)

const errandTaskNoPrefix = "ET"

func CreateTask(ctx context.Context, captainID int64, req *errandv1.CreateTaskRequest) (int64, error) {
	if captainID <= 0 {
		return 0, connect.NewError(connect.CodeUnauthenticated, errors.New("missing captain id"))
	}

	if req == nil || req.StoreId <= 0 || len(req.DemandItems) == 0 {
		return 0, ErrInvalidDemandItem
	}

	selection, err := buildCreateTaskSelection(req)
	if err != nil {
		return 0, err
	}

	var taskID int64
	err = repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		loaded, err := loadCreateTaskData(ctx, tx, req.StoreId, selection)
		if err != nil {
			return err
		}

		task, err := createShoppingTask(ctx, tx, captainID, req.StoreId)
		if err != nil {
			return err
		}
		taskID = task.ID

		taskItemIDByDemandItemID, err := createGroupedTaskItems(ctx, tx, task.ID, loaded.Rows, loaded.Snapshots)
		if err != nil {
			return err
		}

		if err := createTaskAssignments(ctx, tx, task.ID, loaded.Rows, taskItemIDByDemandItemID); err != nil {
			return err
		}

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
	rows, err := repository.LoadSelectedDemandItemsForUpdate(ctx, tx, selection.SelectedIDs)
	if err != nil {
		log.Error().Err(err).Msg("failed to load selected demand items")
		return nil, newErrandInternalError("")
	}
	if len(rows) != len(selection.SelectedIDs) {
		return nil, ErrInvalidDemandItem
	}

	now := time.Now().UTC()
	demandRows := make(map[int64][]repository.SelectedDemandItemRow)
	productIDsSet := make(map[int64]struct{}, len(rows))

	for _, row := range rows {
		if err := validateSelectedDemandItemRow(row, storeID, selection.SelectedUpdatedAt, now); err != nil {
			return nil, err
		}

		demandRows[row.DemandID] = append(demandRows[row.DemandID], row)
		productIDsSet[row.ProductTemplateID] = struct{}{}
	}

	snapshots, err := loadValidatedSnapshots(ctx, storeID, productIDsSet)
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
	storeID int64,
	productIDsSet map[int64]struct{},
) (map[int64]productSnapshot, error) {
	productIDs := make([]int64, 0, len(productIDsSet))
	for id := range productIDsSet {
		productIDs = append(productIDs, id)
	}

	snapshots, err := loadProductSnapshotsFromCatalog(ctx, productIDs)
	if err != nil {
		return nil, newErrandInternalError("")
	}

	if len(snapshots) != len(productIDs) {
		return nil, newErrandInternalError("")
	}

	for _, productID := range productIDs {
		snap, ok := snapshots[productID]
		if !ok {
			return nil, newErrandInternalError("")
		}
		if snap.StoreID != storeID {
			return nil, ErrStoreMismatch
		}
	}

	return snapshots, nil
}

func loadProductSnapshotsFromCatalog(ctx context.Context, productIDs []int64) (map[int64]productSnapshot, error) {
	snapshots := make(map[int64]productSnapshot, len(productIDs))

	resp, err := client.CatalogInternalServiceClient.GetProductTemplates(
		ctx,
		connect.NewRequest(&catalogv1.GetProductTemplatesRequest{
			ProductTemplateIds: productIDs,
		}),
	)
	if err != nil {
		log.Error().
			Err(err).
			Interface("product_template_ids", productIDs).
			Msg("failed to get product templates from catalog service")
		return nil, err
	}

	if resp == nil || resp.Msg == nil {
		log.Error().
			Interface("product_template_ids", productIDs).
			Msg("catalog service returned empty product templates response")
		return nil, newErrandInternalError("")
	}

	for _, template := range resp.Msg.GetProductTemplates() {
		if template == nil {
			log.Error().
				Msg("catalog service returned empty product template")
			return nil, newErrandInternalError("")
		}

		snapshots[template.GetId()] = productSnapshotFromTemplate(template)
	}

	return snapshots, nil
}

func productSnapshotFromTemplate(template *catalogv1.ProductTemplate) productSnapshot {
	return productSnapshot{
		Title:        template.GetTitle(),
		Description:  template.GetDescription(),
		StoreID:      template.GetStoreId(),
		MainImageURL: template.GetMainImageUrl(),
	}
}

func createShoppingTask(ctx context.Context, tx bun.Tx, captainID, storeID int64) (*model.ErrandTask, error) {
	taskNo, err := generateTaskNo()
	if err != nil {
		log.Error().Err(err).Msg("failed to generate errand task number")
		return nil, newErrandInternalError("")
	}

	task := &model.ErrandTask{
		TaskNo:    taskNo,
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

func buildTaskItemGroups(
	rows []repository.SelectedDemandItemRow,
	snapshots map[int64]productSnapshot,
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
	snapshots map[int64]productSnapshot,
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
	selectedItemIDs := make([]int64, 0)
	for demandID, selectedRows := range demandRows {
		demandIDs = append(demandIDs, demandID)
		selectedItemIDs = append(selectedItemIDs, demandItemIDs(selectedRows)...)
	}

	demandIDsWithUnselectedItems, err := repository.LoadDemandIDsWithUnselectedItems(
		ctx,
		tx,
		demandIDs,
		selectedItemIDs,
	)
	if err != nil {
		log.Error().Err(err).Msg("failed to load demand ids with unselected items")
		return newErrandInternalError("")
	}

	for demandID, selectedRows := range demandRows {
		if err := syncSingleDemand(
			ctx,
			tx,
			taskID,
			demandID,
			selectedRows,
			demandIDsWithUnselectedItems,
			now,
		); err != nil {
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
	demandIDsWithUnselectedItems map[int64]struct{},
	now time.Time,
) error {
	selectedItemIDs := demandItemIDs(selectedRows)
	_, hasUnselectedItems := demandIDsWithUnselectedItems[demandID]

	if !hasUnselectedItems {
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

func generateTaskNo() (string, error) {
	return idgen.NewOrderNo(errandTaskNoPrefix)
}

func newErrandInternalError(msg string) error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_ErrandError{
		ErrandError: &errandv1.ErrandError{
			Code: errandv1.ErrandErrorCode_ERRAND_ERROR_CODE_INTERNAL_ERROR,
		},
	}, msg)
}
