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

type ProductSnapshotRow struct {
	ID           int64  `bun:"id"`
	Title        string `bun:"title"`
	Description  string `bun:"description"`
	StoreID      int64  `bun:"store_id"`
	MainImageURL string `bun:"main_image_url"`
}

type ShoppingTaskHeaderRow struct {
	TaskID    int64                  `bun:"task_id"`
	StoreID   int64                  `bun:"store_id"`
	StoreName string                 `bun:"store_name"`
	Status    model.ErrandTaskStatus `bun:"status"`
}

type ShoppingTaskItemRow struct {
	TaskItemID           int64     `bun:"task_item_id"`
	ProductTemplateID    int64     `bun:"product_templated_id"`
	TitleSnapshot        string    `bun:"title_snapshot"`
	DescriptionSnapshot  string    `bun:"description_snapshot"`
	ImageURLSnapshot     string    `bun:"image_url_snapshot"`
	ProductPriceCents    int32     `bun:"product_price_cents"`
	RequiredQuantity     int32     `bun:"required_quantity"`
	PurchasedQuantity    *int32    `bun:"purchased_quantity"`
	NonPurchaseReason    string    `bun:"non_purchase_reason"`
	ActualUnitPriceCents *int32    `bun:"actual_unit_price_cents"`
	UpdatedAt            time.Time `bun:"updated_at"`
}

type ShoppingTaskItemForUpdateRow struct {
    TaskID             int64     `bun:"task_id"`
    TaskStatus         model.ErrandTaskStatus    `bun:"task_status"`
    TaskUpdatedAt      time.Time `bun:"task_updated_at"`
    TaskItemID         int64     `bun:"task_item_id"`
    RequiredQuantity   int32       `bun:"required_quantity"`
    PurchasedQuantity  *int32       `bun:"purchased_quantity"`
    NonPurchaseReason  string    `bun:"non_purchase_reason"`
    TaskItemUpdatedAt  time.Time `bun:"task_item_updated_at"`
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

func LoadProductSnapshots(ctx context.Context, db bun.IDB, ids []int64) (map[int64]ProductSnapshotRow, error) {
	if len(ids) == 0 {
		return map[int64]ProductSnapshotRow{}, nil
	}

	rows := make([]ProductSnapshotRow, 0, len(ids))

	err := db.NewSelect().
		TableExpr("catalog.catalog_product_template as cpt").
		ColumnExpr("cpt.id as id").
		ColumnExpr("cpt.title as title").
		ColumnExpr("cpt.description as description").
		ColumnExpr("cpt.store_id as store_id").
		ColumnExpr(`
		coalesce(
		(select cpi.image_url
		from
		catalog.catalog_product_image as cpi
		where
		cpi.product_template_id = cpt.id
		order by
		cpi.sort_order asc,cpi.id asc
		limit 1),''
		) as main_image_url
	`).
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

func LoadDemandItemCounts(ctx context.Context, db bun.IDB, demandIDs []int64) (map[int64]int, error) {
	if len(demandIDs) == 0 {
		return map[int64]int{}, nil
	}

	type countRow struct {
		DemandID int64 `bun:"demand_id"`
		Cnt      int   `bun:"cnt"` // 明细条数
	}

	var rows []countRow
	err := db.NewSelect().
		TableExpr("errand.errand_demand_item as edi").
		ColumnExpr("edi.errand_demand_id AS demand_id").
		ColumnExpr("count(*) as cnt").
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

func GetShoppingTaskHeader(ctx context.Context, db bun.IDB, taskID, captainID int64) (*ShoppingTaskHeaderRow, error) {
	var row ShoppingTaskHeaderRow
	err := db.NewSelect().TableExpr("errand.errand_task as et").
		Join("left join catalog.catalog_store as cs").
		ColumnExpr("et.id as task_id,").
		ColumnExpr("et.id as task_id,").
		ColumnExpr("coalesce(cs.name, '') as store_name").
		Where("et.id = ?", taskID).Where("et.captain_id = ? ", captainID).Limit(1).Scan(ctx, &row)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func ListShoppingTaskItems(ctx context.Context, db bun.IDB, taskID int64) ([]ShoppingTaskItemRow, error) {
	rows := make([]ShoppingTaskItemRow, 0)
	err := db.NewSelect().
		TableExpr("errand.errand_task_item as eti").
		Join("left join catalog.catalog_product_template as cpt").
		ColumnExpr("eti.id as task_item_id").
		ColumnExpr("eti.product_template_id as product_template_id").
		ColumnExpr("eti.title_snapshot as title_snapshot").
		ColumnExpr("eti.description_snapshot as description_snapshot").
		ColumnExpr("eti.image_url_snapshot as image_url_snapshot").
		ColumnExpr("eti.product_price_cents as product_price_cents").
		ColumnExpr("eti.required_quantity as required_quantity").
		ColumnExpr("eti.non_purchase_reason as non_purchase_reason").
		ColumnExpr("eti.actual_unit_price_cents as actual_unit_price_cents").
		ColumnExpr("eti.updated_at as updated_at").
		Where("eti.task_id = ?", taskID).OrderExpr("eti.deadline asc, eit.id asc").Scan(ctx, &rows)

	return rows, err
}
func GetShoppingTaskItemForUpdate(ctx context.Context, db bun.IDB, taskID, taskItemID, captainID int64) (*ShoppingTaskItemForUpdateRow, error) {
	var row ShoppingTaskItemForUpdateRow
	err := db.NewSelect().
	TableExpr("errand.errand_task_item as eti").
	Join("join errand.errand_task as on et.id = eti.task_id").
	ColumnExpr("et.id as task_id").
	ColumnExpr("et.status as task_status").
	ColumnExpr("et.updated_at as task_updated_at").
	ColumnExpr("eti.id as task_item_id").
	ColumnExpr("eti.required_quantity as required_quantity").
	ColumnExpr("eti.purchased_quantity as purchased_quantity").
	ColumnExpr("eti.non_purchase_reason as non_purchase_reason").
	ColumnExpr("eti.updated_at as task_item_updated_at").Where("et.id = ?",taskID).Where("eti.id = ?", taskItemID).Where("et.captain_id = ?",captainID).Limit(1).For("update").Scan(ctx, &row)
	if err != nil {
		return nil, err
	}
	return &row,nil
}

func UpdateShoppingTaskItem(ctx context.Context, db bun.IDB, taskItemID int64, expectedUpdatedAt time.Time, purchasedQuantity int32, nonPurchaseReason string, now time.Time) error {
	res, err := db.NewUpdate().Model((*model.ErrandTaskItem)(nil)).Set("purchased_quantity = ?",purchasedQuantity).Set("non_purchase_reason = ? ",nonPurchaseReason).Set("updated_at = ? ",now).
	Where("id = ? ", taskItemID).Where("updated_at = ? ",expectedUpdatedAt).Exec(ctx)
	if err != nil{
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil{
		return err
	}
	//1. 行已删除。2. 乐观锁冲突
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}