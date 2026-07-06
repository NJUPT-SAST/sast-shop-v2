package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/uptrace/bun"
)

//联合查询子项和主单，承载单条子项 + 所属主单全量数据库字段，用于所有校验、分组逻辑
type SelectedDemandItemRow struct{
	DemandItemID int64 `bun:"demand_item_id"`
	DemandItemUpdatedAt time.Time `bun:"demand_item_updated_at"` //乐观锁
	DemandItemStatus model.ErrandDemandItemStatus `bun:"demand_item_status"` //校验open状态
	DemandID int64 `bun:"demand_id"`
	DemandStatus model.ErrandDemandStatus `bun:"demand_status"`
	RequesterID int64 `bun:"requester_id"`
	StoreID int64 `bun:"store_id"`
	ProductTemplateID int64 `bun:"product_template_id"` //聚合task_item
	Quantity int32 `bun:"quantity"`
	ServiceFeePerUnitCents int32 `bun:"service_fee_per_unit_cents"`
	Deadline time.Time `bun:"deadline"`
}
//创建任务时把商品详情存进任务明细，防止后续商品修改导致历史任务展示错乱
type ProductSnapshotRow struct{
	ID int64 `bun:"id"`
	Title string `bun:"tittle"`
	Description string `bun:"description"`
	StoreID int64 `bun:"store_id"`
	MainImageURL string `bun:"main_image_url"` 
}


func RunInTx(ctx context.Context,fn func(ctx context.Context,tx bun.Tx)error) error{
	return postgres.DB.RunInTx(ctx,&sql.TxOptions{},fn)

}

func LoadSelectedDemandItemsForUpdate(ctx context.Context,db bun.IDB,ids []int64)([]SelectedDemandItemRow,error){
	rows := make([]SelectedDemandItemRow, 0, len(ids))
	err := db.NewSelect().
		TableExpr("errand.errand_demand_item AS edi").
		Join("JOIN errand.errand_demand AS ed ON ed.id = edi.errand_demand_id").
		ColumnExpr("edi.id AS demand_item_id").
		ColumnExpr("edi.updated_at AS demand_item_updated_at").
		ColumnExpr("edi.status AS demand_item_status").
		ColumnExpr("ed.id AS demand_id").
		ColumnExpr("ed.status AS demand_status").
		ColumnExpr("edi.requester_id AS requester_id").
		ColumnExpr("edi.store_id AS store_id").
		ColumnExpr("edi.product_template_id AS product_template_id").
		ColumnExpr("edi.quantity AS quantity").
		ColumnExpr("edi.service_fee_per_unit_cents AS service_fee_per_unit_cents").
		ColumnExpr("edi.estimated_unit_price_cents AS estimated_unit_price_cents").
		ColumnExpr("ed.deadline AS deadline").
		Where("edi.id IN (?)", bun.List(ids)).
		OrderExpr("edi.id ASC").
		For("UPDATE").
		Scan(ctx, &rows)
	return rows, err
}

func LoadProductSnapshots(ctx context.Context, db bun.IDB, ids []int64) (map[int64]ProductSnapshotRow, error) {
	if len(ids) == 0 {
		return map[int64]ProductSnapshotRow{}, nil
	}

	rows := make([]ProductSnapshotRow, 0, len(ids))
	err := db.NewSelect().
		TableExpr("catalog.catalog_product_template AS cpt").
		ColumnExpr("cpt.id AS id").
		ColumnExpr("cpt.title AS title").
		ColumnExpr("cpt.description AS description").
		ColumnExpr("cpt.store_id AS store_id").
		ColumnExpr(`COALESCE((
			SELECT cpi.image_url
			FROM catalog.catalog_product_image AS cpi
			WHERE cpi.product_template_id = cpt.id
			ORDER BY cpi.sort_order ASC, cpi.id ASC
			LIMIT 1
		), '') AS main_image_url`).
		Where("cpt.id IN (?)", bun.List(ids)).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	result := make(map[int64]ProductSnapshotRow, len(rows))
	for _, row := range rows {
		result[row.ID] = row
	}
	return result, nil
}


func LoadDemandItemCounts(ctx context.Context, db bun.IDB, demandIDs []int64) (map[int64]int, error) {
	if len(demandIDs) == 0 {
		return map[int64]int{}, nil
	}

	type countRow struct {
		DemandID int64 `bun:"demand_id"`
		Cnt      int   `bun:"cnt"`
	}

	var rows []countRow
	err := db.NewSelect().
		TableExpr("errand.errand_demand_item AS edi").
		ColumnExpr("edi.errand_demand_id AS demand_id").
		ColumnExpr("COUNT(*) AS cnt").
		Where("edi.errand_demand_id IN (?)", bun.List(demandIDs)).
		GroupExpr("edi.errand_demand_id").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	result := make(map[int64]int, len(rows))
	for _, row := range rows {
		result[row.DemandID] = row.Cnt
	}
	return result, nil
}

func CreateTask(ctx context.Context, db bun.IDB, task *model.ErrandTask) error {
	_, err := db.NewInsert().Model(task).Returning("id").Exec(ctx)
	return err
}

func CreateDemand(ctx context.Context, db bun.IDB, demand *model.ErrandDemand) error {
	_, err := db.NewInsert().Model(demand).Returning("id").Exec(ctx)
	return err
}

func UpdateDemandToShopping(ctx context.Context, db bun.IDB, demandID, taskID int64, now time.Time) error {
	_, err := db.NewUpdate().
		Model((*model.ErrandDemand)(nil)).
		Set("status = ?", model.ErrandDemandStatusShopping).
		Set("task_id = ?", taskID).
		Set("shopping_start_at = ?", now).
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
		Where("id IN (?)", bun.List(itemIDs)).
		Exec(ctx)
	return err
}