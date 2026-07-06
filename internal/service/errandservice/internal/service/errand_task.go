package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/repository"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
)

type taskItemGroupKey struct {
	ProductTemplateID int64
	Deadline          time.Time
}

type taskItemGroup struct {
	Snapshot         repository.ProductSnapshotRow
	RequiredQuantity int32
	Rows             []repository.SelectedDemandItemRow
}

var (
	ErrInvalidDemandItem   = errors.New("invalid demand item")
	ErrConcurrencyConflict = errors.New("concurrency conflict")
	ErrStoreMismatch       = errors.New("store mismatch")
	ErrDemandItemNotOpen   = errors.New("demand item not open")
)

//nolint:gocyclo
func CreateTask(ctx context.Context, captainID int64, req *errandv1.CreateTaskRequest) (int64, error) {
	if captainID <= 0 {
		return 0, connect.NewError(connect.CodeUnauthenticated, errors.New("missing captain id"))
	}
	if req == nil || req.StoreId <= 0 || len(req.DemandItems) == 0 {
		return 0, ErrInvalidDemandItem
	}

	selectedUpdatedAt := make(map[int64]time.Time, len(req.DemandItems))
	selectedIDs := make([]int64, 0, len(req.DemandItems))
	for i, item := range req.DemandItems {
		if item == nil || item.ErrandDemandItemId <= 0 || item.UpdatedAt == nil || !item.UpdatedAt.IsValid() {
			return 0, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("demand_items[%d] is invalid", i))
		}
		if _, dup := selectedUpdatedAt[item.ErrandDemandItemId]; dup {
			return 0, ErrInvalidDemandItem
		}
		selectedUpdatedAt[item.ErrandDemandItemId] = item.UpdatedAt.AsTime().UTC()
		selectedIDs = append(selectedIDs, item.ErrandDemandItemId)
	}

	var taskID int64
	err := repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		rows, err := repository.LoadSelectedDemandItemsForUpdate(ctx, tx, selectedIDs)
		if err != nil {
			log.Error().Err(err).Msg("failed to load selected demand items")
			return newErrandInternalError("")
		}
		if len(rows) != len(selectedIDs) {
			return ErrInvalidDemandItem
		}

		now := time.Now().UTC()
		demandRows := make(map[int64][]repository.SelectedDemandItemRow)
		productIDsSet := make(map[int64]struct{}, len(rows))

		for _, row := range rows {
			if row.StoreID != req.StoreId {
				return ErrStoreMismatch
			}
			if row.DemandStatus != model.ErrandDemandStatusOpen ||
				row.DemandItemStatus != model.ErrandDemandItemStatusOpen {
				return ErrDemandItemNotOpen
			}
			if !row.DemandItemUpdatedAt.UTC().Equal(selectedUpdatedAt[row.DemandItemID]) {
				return ErrConcurrencyConflict
			}
			if !row.Deadline.After(now) {
				return ErrInvalidDemandItem
			}

			demandRows[row.DemandID] = append(demandRows[row.DemandID], row)
			productIDsSet[row.ProductTemplateID] = struct{}{}
		}

		productIDs := make([]int64, 0, len(productIDsSet))
		for id := range productIDsSet {
			productIDs = append(productIDs, id)
		}

		snapshots, err := repository.LoadProductSnapshots(ctx, tx, productIDs)
		if err != nil {
			log.Error().Err(err).Msg("failed to load product snapshots")
			return newErrandInternalError("")
		}
		if len(snapshots) != len(productIDs) {
			return newErrandInternalError("product snapshot missing")
		}
		for _, snap := range snapshots {
			if snap.StoreID != req.StoreId {
				return ErrStoreMismatch
			}
		}

		task := &model.ErrandTask{
			TaskNo:    generateTaskNo(),
			CaptainID: captainID,
			StoreID:   req.StoreId,
			Status:    model.ErrandTaskStatusShopping,
		}
		if err := repository.CreateTask(ctx, tx, task); err != nil {
			log.Error().Err(err).Msg("failed to create task")
			return newErrandInternalError("")
		}
		taskID = task.ID

		grouped := make(map[taskItemGroupKey]*taskItemGroup)
		for _, row := range rows {
			key := taskItemGroupKey{
				ProductTemplateID: row.ProductTemplateID,
				Deadline:          row.Deadline.UTC(),
			}
			group, ok := grouped[key]
			if !ok {
				group = &taskItemGroup{
					Snapshot: snapshots[row.ProductTemplateID],
				}
				grouped[key] = group
			}
			group.RequiredQuantity += row.Quantity
			group.Rows = append(group.Rows, row)
		}

		taskItemIDByDemandItemID := make(map[int64]int64, len(rows))
		for key, group := range grouped {
			taskItem := &model.ErrandTaskItem{
				TaskID:              task.ID,
				ProductTemplateID:   key.ProductTemplateID,
				TitleSnapshot:       group.Snapshot.Title,
				DescriptionSnapshot: group.Snapshot.Description,
				ImageURLSnapshot:    group.Snapshot.MainImageURL,
				RequiredQuantity:    group.RequiredQuantity,
				Deadline:            key.Deadline,
			}
			if err := repository.CreateTaskItem(ctx, tx, taskItem); err != nil {
				log.Error().Err(err).Msg("failed to create task item")
				return newErrandInternalError("")
			}
			for _, row := range group.Rows {
				taskItemIDByDemandItemID[row.DemandItemID] = taskItem.ID
			}
		}

		assignments := make([]*model.ErrandTaskAssignment, 0, len(rows))
		for _, row := range rows {
			assignments = append(assignments, &model.ErrandTaskAssignment{
				TaskID:                 task.ID,
				TaskItemID:             taskItemIDByDemandItemID[row.DemandItemID],
				DemandItemID:           row.DemandItemID,
				PurchaserID:            row.RequesterID,
				ServiceFeePerUnitCents: row.ServiceFeePerUnitCents,
			})
		}
		if err := repository.CreateTaskAssignments(ctx, tx, assignments); err != nil {
			log.Error().Err(err).Msg("failed to create task assignments")
			return newErrandInternalError("")
		}

		demandIDs := make([]int64, 0, len(demandRows))
		for demandID := range demandRows {
			demandIDs = append(demandIDs, demandID)
		}

		itemCountByDemandID, err := repository.LoadDemandItemCounts(ctx, tx, demandIDs)
		if err != nil {
			log.Error().Err(err).Msg("failed to count demand items")
			return newErrandInternalError("")
		}

		for demandID, selectedRows := range demandRows {
			totalCount, ok := itemCountByDemandID[demandID]
			if !ok || totalCount == 0 {
				return newErrandInternalError("demand item count missing")
			}

			selectedItemIDs := demandItemIDs(selectedRows)
			if totalCount == len(selectedRows) {
				if err := repository.UpdateDemandToShopping(ctx, tx, demandID, task.ID, now); err != nil {
					log.Error().Err(err).Msg("failed to update full-selected demand")
					return newErrandInternalError("")
				}
				if err := repository.UpdateDemandItemsToShopping(ctx, tx, selectedItemIDs, now); err != nil {
					log.Error().Err(err).Msg("failed to update full-selected demand items")
					return newErrandInternalError("")
				}
				continue
			}

			base := selectedRows[0]
			taskIDCopy := task.ID
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
			if err := repository.MoveDemandItemsToDemandAndShopping(
				ctx,
				tx,
				selectedItemIDs,
				splitDemand.ID,
				now,
			); err != nil {
				log.Error().Err(err).Msg("failed to move selected items to split demand")
				return newErrandInternalError("")
			}
			if err := repository.TouchDemandUpdatedAt(ctx, tx, demandID, now); err != nil {
				log.Error().Err(err).Msg("failed to touch original demand")
				return newErrandInternalError("")
			}
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return taskID, nil
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
