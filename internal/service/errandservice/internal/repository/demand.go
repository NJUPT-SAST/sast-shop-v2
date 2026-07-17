package repository

import (
	"context"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/uptrace/bun"
)

func CreateDemand(ctx context.Context, demand *model.ErrandDemand) (int64, error) {
	demand.Status = model.ErrandDemandStatusOpen
	demand.CreatedAt = time.Now()
	demand.UpdatedAt = time.Now()

	_, err := postgres.DB.NewInsert().
		Model(demand).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	return demand.ID, nil
}

func BatchCreateDemandItems(ctx context.Context, items []*model.ErrandDemandItem) error {
	if len(items) == 0 {
		return nil
	}

	now := time.Now()
	for _, item := range items {
		item.Status = model.ErrandDemandItemStatusOpen
		item.CreatedAt = now
		item.UpdatedAt = now
	}

	_, err := postgres.DB.NewInsert().
		Model(&items).
		Exec(ctx)
	return err
}

type DemandListAggregation struct {
	StoreID                   int64     `bun:"store_id"`
	TotalOriginUnitPriceCents int32     `bun:"total_origin_unit_price_cents"`
	TotalServiceFeeCents      int32     `bun:"total_service_fee_cents"`
	LatestUpdatedAt           time.Time `bun:"latest_updated_at"`
}

func GetDemandListByStore(
	ctx context.Context,
	page, pageSize int32,
	storeName string,
) ([]*DemandListAggregation, int, error) {
	query := postgres.DB.NewSelect().
		ColumnExpr("store_id").
		ColumnExpr("SUM(estimated_unit_price_cents * quantity) AS total_origin_unit_price_cents").
		ColumnExpr("SUM(service_fee_per_unit_cents * quantity) AS total_service_fee_cents").
		ColumnExpr("MAX(updated_at) AS latest_updated_at").
		TableExpr("errand.errand_demand_item").
		Where("status = ?", model.ErrandDemandItemStatusOpen).
		Group("store_id").
		Order("latest_updated_at DESC")

	totalCount, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	var results []*DemandListAggregation
	err = query.
		Limit(int(pageSize)).
		Offset(int(offset)).
		Scan(ctx, &results)

	return results, totalCount, err
}

func GetDistinctRequestersByStore(
	ctx context.Context,
	storeID int64,
	limit int,
) ([]int64, error) {
	var requesterIDs []int64
	err := postgres.DB.NewSelect().
		ColumnExpr("DISTINCT requester_id").
		TableExpr("errand.errand_demand_item").
		Where("status = ?", model.ErrandDemandItemStatusOpen).
		Where("store_id = ?", storeID).
		Limit(limit).
		Scan(ctx, &requesterIDs)
	return requesterIDs, err
}

func GetOpenDemandItemsByStore(
	ctx context.Context,
	storeID int64,
) ([]*model.ErrandDemandItem, error) {
	var items []*model.ErrandDemandItem
	err := postgres.DB.NewSelect().
		Model(&items).
		Where("store_id = ?", storeID).
		Where("status = ?", model.ErrandDemandItemStatusOpen).
		Order("product_template_id ASC", "updated_at DESC").
		Scan(ctx)
	return items, err
}

func GetDemandByID(ctx context.Context, demandID int64) (*model.ErrandDemand, error) {
	var demand model.ErrandDemand
	err := postgres.DB.NewSelect().
		Model(&demand).
		Where("id = ?", demandID).
		Scan(ctx)
	return &demand, err
}

func GetDemandsByRequester(
	ctx context.Context,
	requesterID int64,
	storeID *int64,
	status *model.ErrandDemandStatus,
	page, pageSize int32,
) ([]*model.ErrandDemand, int, error) {
	query := postgres.DB.NewSelect().
		Model((*model.ErrandDemand)(nil)).
		Where("requester_id = ?", requesterID).
		Order("created_at DESC")

	if storeID != nil {
		query.Where("store_id = ?", *storeID)
	}
	if status != nil {
		query.Where("status = ?", *status)
	}

	totalCount, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	var demands []*model.ErrandDemand
	err = query.
		Limit(int(pageSize)).
		Offset(int(offset)).
		Scan(ctx, &demands)
	return demands, totalCount, err
}

func GetDemandItemsByDemandIDs(ctx context.Context, demandIDs []int64) ([]*model.ErrandDemandItem, error) {
	if len(demandIDs) == 0 {
		return nil, nil
	}
	var items []*model.ErrandDemandItem
	err := postgres.DB.NewSelect().
		Model(&items).
		Where("errand_demand_id IN (?)", bun.List(demandIDs)).
		Order("product_template_id ASC").
		Scan(ctx)
	return items, err
}

func GetAssignmentsByDemandItemIDs(ctx context.Context, demandItemIDs []int64) ([]*model.ErrandTaskAssignment, error) {
	if len(demandItemIDs) == 0 {
		return nil, nil
	}
	var assignments []*model.ErrandTaskAssignment
	err := postgres.DB.NewSelect().
		Model(&assignments).
		Where("demand_item_id IN (?)", bun.List(demandItemIDs)).
		Scan(ctx)
	return assignments, err
}

func GetTaskItemsByTaskID(ctx context.Context, taskID int64) ([]*model.ErrandTaskItem, error) {
	var items []*model.ErrandTaskItem
	err := postgres.DB.NewSelect().
		Model(&items).
		Where("task_id = ?", taskID).
		Scan(ctx)
	return items, err
}
