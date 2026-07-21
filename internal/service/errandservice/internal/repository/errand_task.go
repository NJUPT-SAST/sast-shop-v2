package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/uptrace/bun"
)

type SelectedDemandItemRow struct {
	DemandItemID           int64                        `bun:"demand_item_id"`
	DemandItemUpdatedAt    time.Time                    `bun:"demand_item_updated_at"`
	DemandItemStatus       model.ErrandDemandItemStatus `bun:"demand_item_status"`
	DemandID               int64                        `bun:"demand_id"`
	DemandStatus           model.ErrandDemandStatus     `bun:"demand_status"`
	RequesterID            int64                        `bun:"requester_id"`
	StoreID                int64                        `bun:"store_id"`
	ProductTemplateID      int64                        `bun:"product_template_id"`
	RequiredQuantity       int32                        `bun:"required_quantity"`          // 或 float64
	ServiceFeePerUnitCents int32                        `bun:"service_fee_per_unit_cents"` // 单位：分
	Deadline               time.Time                    `bun:"deadline"`
}

func RunInTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error {
	return postgres.DB.RunInTx(ctx, &sql.TxOptions{}, fn) // 开启事务->执行fn->无错自动commit->fn返回error自动rollback
}

func LoadSelectedDemandItemsForUpdate(ctx context.Context, db bun.IDB, ids []int64) ([]SelectedDemandItemRow, error) {
	rows := make([]SelectedDemandItemRow, 0, len(ids))
	err := db.NewSelect().
		TableExpr("errand.errand_demand_item as edi").
		Join("join errand.errand_demand as ed on ed.id = edi.errand_demand_id").
		ColumnExpr("edi.id AS demand_item_id").
		ColumnExpr("edi.updated_at AS demand_item_updated_at").
		ColumnExpr("edi.status AS demand_item_status").
		ColumnExpr("ed.id AS demand_id").
		ColumnExpr("ed.status AS demand_status").
		ColumnExpr("edi.requester_id AS requester_id").
		ColumnExpr("edi.store_id AS store_id").
		ColumnExpr("edi.product_template_id AS product_template_id").
		ColumnExpr("edi.quantity AS required_quantity").
		ColumnExpr("edi.service_fee_per_unit_cents AS service_fee_per_unit_cents").
		ColumnExpr("ed.deadline AS deadline").
		Where("edi.id IN (?)", bun.List(ids)).
		OrderExpr("edi.id ASC").
		For("update").
		Scan(ctx, &rows)

	return rows, err
}

func CreateTask(ctx context.Context, db bun.IDB, task *model.ErrandTask) error {
	_, err := db.NewInsert().
		Model(task).
		Returning("id").
		Exec(ctx)
	return err
}

func CreateTaskItem(ctx context.Context, db bun.IDB, taskItem *model.ErrandTaskItem) error {
	_, err := db.NewInsert().
		Model(taskItem).
		Returning("id").
		Exec(ctx)
	return err
}

func CreateTaskAssigniments(ctx context.Context, db bun.IDB, assignments []*model.ErrandTaskAssignment) error {
	if len(assignments) == 0 {
		return nil
	}

	_, err := db.NewInsert().
		Model(&assignments).
		Exec(ctx)

	return err
}

func LoadDemandIDsWithUnselectedItems(
	ctx context.Context,
	db bun.IDB,
	demandIDs []int64,
	selectedItemIDs []int64,
) (map[int64]struct{}, error) {
	if len(demandIDs) == 0 {
		return map[int64]struct{}{}, nil
	}

	type demandIDRow struct {
		DemandID int64 `bun:"demand_id"`
	}

	var rows []demandIDRow
	query := db.NewSelect().
		TableExpr("errand.errand_demand_item as edi").
		ColumnExpr("DISTINCT edi.errand_demand_id AS demand_id").
		Where("edi.errand_demand_id IN (?)", bun.List(demandIDs))

	if len(selectedItemIDs) > 0 {
		query = query.Where("edi.id NOT IN (?)", bun.List(selectedItemIDs))
	}

	err := query.Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	result := make(map[int64]struct{}, len(rows))
	for _, row := range rows {
		result[row.DemandID] = struct{}{}
	}
	return result, nil
}

func UpdateDemandToShopping(ctx context.Context, db bun.IDB, demandID, taskID int64, now time.Time) error {
	_, err := db.NewUpdate().
		Model((*model.ErrandDemand)(nil)).
		Set("status = ?", model.ErrandDemandStatusShopping).
		Set("task_id =  ?", taskID).
		Set("shopping_start_at = ? ", now).
		Set("updated_at = ?", now).
		Where("id = ?", demandID).
		Exec(ctx)
	return err
}

func UpdateDemandItemsToShopping(ctx context.Context, db bun.IDB, itemIDs []int64, now time.Time) error {
	if len(itemIDs) == 0 {
		return nil
	}

	_, err := db.NewUpdate().
		Model((*model.ErrandDemandItem)(nil)).
		Set("status = ?", model.ErrandDemandItemStatusShopping).
		Set("updated_at = ?", now).
		Where("id in (?)", bun.List(itemIDs)).
		Exec(ctx)
	return err
}

func CreateDemand(ctx context.Context, db bun.IDB, demand *model.ErrandDemand) error {
	_, err := db.NewInsert().
		Model(demand).
		Returning("id").
		Exec(ctx)
	return err
}

func MoveDemandItemsToDemandAndShopping(ctx context.Context,
	db bun.IDB,
	itemIDs []int64,
	demandID int64,
	now time.Time,
) error {
	if len(itemIDs) == 0 {
		return nil
	}
	_, err := db.NewUpdate().
		Model((*model.ErrandDemandItem)(nil)).
		Set("errand_demand_id = ?", demandID).
		Set("status = ?", model.ErrandDemandItemStatusShopping).
		Set("updated_at = ?", now).
		Where("id in (?)", bun.List(itemIDs)).
		Exec(ctx)
	return err
}

func TouchDemandUpdatedAt(ctx context.Context, db bun.IDB, demandID int64, now time.Time) error {
	_, err := db.NewUpdate().
		Model((*model.ErrandDemand)(nil)).
		Set("updated_at = ?", now).
		Where("id = ?", demandID).
		Exec(ctx)
	return err
}
