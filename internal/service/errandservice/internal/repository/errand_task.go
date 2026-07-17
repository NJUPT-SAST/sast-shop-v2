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
	TaskID            int64                  `bun:"task_id"`
	TaskStatus        model.ErrandTaskStatus `bun:"task_status"`
	TaskUpdatedAt     time.Time              `bun:"task_updated_at"`
	TaskItemID        int64                  `bun:"task_item_id"`
	RequiredQuantity  int32                  `bun:"required_quantity"`
	PurchasedQuantity *int32                 `bun:"purchased_quantity"`
	NonPurchaseReason string                 `bun:"non_purchase_reason"`
	TaskItemUpdatedAt time.Time              `bun:"task_item_updated_at"`
}

func RunInTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error {
	return postgres.DB.RunInTx(ctx, &sql.TxOptions{}, fn) // 开启事务->执行fn->无错自动commit->fn返回error自动rollback
}

func LoadSelectedDemandItemsForUpdate(ctx context.Context, db bun.IDB, ids []int64) ([]SelectedDemandItemRow, error) {
	rows := make([]SelectedDemandItemRow, 0, len(ids))
	err := db.NewSelect().
		TableExpr("errand.errand_demand_item as edi").
		Join("join errand.errand_demand as ed on ed.id = edi.errand_demand_id"). // 内连接ed，一次查询即可获取需求项本身及其需求单的字段
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
		OrderExpr("edi.id ASC"). // 按需求项升序排列
		For("update").           // 查询返回的每一行都会被锁定，直到当前事务repository.RunInTx提交或回滚
		Scan(ctx, &rows)

	return rows, err
}

func LoadProductSnapshots(ctx context.Context, db bun.IDB, ids []int64) (map[int64]ProductSnapshotRow, error) {
	// 前面productId没有校验过，可能为空（？
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
		ColumnExpr(
			`  
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
	`,
		). // 关联子查询，用于单条数据联表查询
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
		Model(task).     // 使用Model(task)时，反射分析task的类型，自动生成表名，并插入task数据
		Returning("id"). // 在插入数据后将数据库自动生成的值返回到go结构体
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

func GetShoppingTaskItemForUpdate(
	ctx context.Context,
	db bun.IDB,
	taskID, taskItemID, captainID int64,
) (*ShoppingTaskItemForUpdateRow, error) {
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
		ColumnExpr("eti.updated_at as task_item_updated_at").
		Where("et.id = ?", taskID).
		Where("eti.id = ?", taskItemID).Where("et.captain_id = ?", captainID).Limit(1).For("update").Scan(ctx, &row)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func UpdateShoppingTaskItem(
	ctx context.Context,
	db bun.IDB,
	taskItemID int64,
	expectedUpdatedAt time.Time,
	purchasedQuantity int32,
	nonPurchaseReason string,
	now time.Time,
) error {
	res, err := db.NewUpdate().
		Model((*model.ErrandTaskItem)(nil)).
		Set("purchased_quantity = ?", purchasedQuantity).
		Set("non_purchase_reason = ? ", nonPurchaseReason).
		Set("updated_at = ? ", now).
		Where("id = ? ", taskItemID).
		Where("updated_at = ? ", expectedUpdatedAt).
		Exec(ctx)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	// 1. 行已删除。2. 乐观锁冲突
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

type ErrandTaskForUpdateRow struct {
	TaskID    int64                  `bun:"task_id"`
	CaptainID int64                  `bun:"captain_id"`
	Status    model.ErrandTaskStatus `bun:"status"`
	UpdatedAt time.Time              `bun:"updated_at"`
}

func GetErrandTaskForUpdate(ctx context.Context, db bun.IDB, taskID, captainID int64) (*ErrandTaskForUpdateRow, error) {
	var row ErrandTaskForUpdateRow
	err := db.NewSelect().
		TableExpr("errand.errand_task as et").
		ColumnExpr("et.id as task_id").
		ColumnExpr("et.captain_id as captain_id").
		ColumnExpr("et.status as status").
		ColumnExpr("et.updated_at as updated_at").
		Where("et.id = ?", taskID).
		Where("et.captain_id = ?", captainID).
		Limit(1).
		For("update").
		Scan(ctx, &row)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func GetErrandTaskForUpdateByID(ctx context.Context, db bun.IDB, taskID int64) (*ErrandTaskForUpdateRow, error) {
	var row ErrandTaskForUpdateRow
	err := db.NewSelect().
		TableExpr("errand.errand_task as et").
		ColumnExpr("et.id as task_id").
		ColumnExpr("et.captain_id as captain_id").
		ColumnExpr("et.status as status").
		ColumnExpr("et.updated_at as updated_at").
		Where("et.id = ?", taskID).
		Limit(1).
		For("update").
		Scan(ctx, &row)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

type TaskItemHandlingSummaryRow struct {
	TotalCount     int64 `bun:"total_count"`
	UnhandledCount int64 `bun:"unhandled_count"`
}

func GetTaskItemHandlingSummary(ctx context.Context, db bun.IDB, taskID int64) (*TaskItemHandlingSummaryRow, error) {
	var row TaskItemHandlingSummaryRow
	err := db.NewSelect().
		TableExpr("errand.errand_task_item as eti").
		ColumnExpr("count(*) as total_count").
		//统计团长还没有处理和标记的商品
		ColumnExpr("count(*) filter (where eti.purchased_quantity is null) as unhandled_count").
		Where("eti.task_id = ?", taskID).
		Scan(ctx, &row)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func UpdateTaskToPendingDistributing(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
	expectedUpdatedAt time.Time,
	now time.Time,
) error {
	res, err := db.NewUpdate().
		Model((*model.ErrandTask)(nil)).
		Set("status = ?", model.ErrandTaskStatusPendingDistributing).
		Set("shopping_conpleted_at = ? ", now).
		Set("updated_at = ?", now).
		Where("id = ?", taskID).
		Where("status = ?", model.ErrandDemandStatusShopping).
		Where("updated_at = ?", expectedUpdatedAt).
		Exec(ctx)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	// 乐观锁冲突/当前状态不是采购中/任务不存在
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func UpdateTaskRelatedDemandsToPendingDistributing(ctx context.Context, db bun.IDB, taskID int64, now time.Time) error {
	_, err := db.NewUpdate().
		// 用空指针传入模型，让bun根据结构体标签推断表名
		Model((*model.ErrandDemandItem)(nil)).
		Set("status = ?", model.ErrandTaskStatusPendingDistributing).
		Set("updated_at = ?", now).
		// 选择同一次跑腿任务下对应的所有demand_item_id,让demand_item表里的id等与这些
		Where("id in (select eta.demand_item_id from errand.errand_task_assignment as eta where eta.task_id = ?)", taskID).
		Where("status = ?", model.ErrandDemandStatusShopping).
		Exec(ctx)
	return err
}

// 先在事务里取出需要通知的 demand_item
// RunInTx 成功后再发
type NonPurchasedDemandItemNotificationRow struct {
	TaskItemID        int64  `bun:"task_item_id"`
	TitleSnapshot     string `bun:"title_snapshot"`
	PurchasedQuantity int32  `bun:"purchased_quantity"`
	DemandItemID      int64  `bun:"demand_item_id"`
	RequesterID       int64  `bun:"requester_id"`
	RequiredQuantity  int32  `bun:"required_quantity"`
	RequesterName     string `bun:"requester_name"`
	RequesterOpenID   string `bun:"requester_open_id"`
}
//列出任务中未完成购买的需求项通知
func ListNonPurchasedDemandItemNotifications(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
) ([]NonPurchasedDemandItemNotificationRow, error) {
	rows := make([]NonPurchasedDemandItemNotificationRow, 0)
	err := db.NewSelect().
		TableExpr("errand.errand_task_item AS eti").
		Join("JOIN errand.errand_task_assignment AS eta ON eta.task_item_id = eti.id").
		Join("JOIN errand.errand_demand_item AS edi ON edi.id = eta.demand_item_id").
		Join("LEFT JOIN user.user_account AS ua ON ua.id = edi.requester_id").
		ColumnExpr("eti.id AS task_item_id").
		ColumnExpr("eti.title_snapshot AS title_snapshot").
		ColumnExpr("COALESCE(eti.purchased_quantity, 0) AS purchased_quantity").
		ColumnExpr("edi.id AS demand_item_id").
		ColumnExpr("edi.requester_id AS requester_id").
		ColumnExpr("edi.quantity AS required_quantity").
		ColumnExpr("COALESCE(ua.display_name, '') AS requester_name").
		ColumnExpr("COALESCE(ua.feishu_open_id, '') AS requester_open_id").
		Where("eti.task_id = ?", taskID).
		OrderExpr("eti.id ASC, edi.id ASC").
		Scan(ctx, &rows)
	return rows, err
}

type DistributingTaskHeaderRow struct {
	TaskID            int64                  `bun:"task_id"`
	StoreID           int64                  `bun:"store_id"`
	StoreName         string                 `bun:"store_name"`
	PackagingFeeCents int32                  `bun:"packaging_fee_cents"`
	Status            model.ErrandTaskStatus `bun:"status"`
}

type DistributingTaskDetailRow struct {
	TaskItemID              int64     `bun:"task_item_id"`
	ProductTemplateID       int64     `bun:"product_template_id"`
	TitleSnapshot           string    `bun:"title_snapshot"`
	DescriptionSnapshot     string    `bun:"description_snapshot"`
	ImageURLSnapshot        string    `bun:"image_url_snapshot"`
	OriginUnitPriceCents    int32     `bun:"origin_unit_price_cents"`
	ActualUnitPriceCents    *int32    `bun:"actual_unit_price_cents"`
	PurchaserID             int64     `bun:"purchaser_id"`
	PurchaserName           string    `bun:"purchaser_name"`
	PurchaserAvatarURL      string    `bun:"purchaser_avatar_url"`
	Quantity                int32     `bun:"quantity"`
	DistributedQuantity     int32     `bun:"distributed_quantity"`
	TaskAssignmentID        int64     `bun:"task_assignment_id"`
	DemandItemID            int64     `bun:"demand_item_id"`
	TaskAssignmentUpdatedAt time.Time `bun:"task_assignment_updated_at"`
}

type DistributingTaskItemForUpdateRow struct {
	TaskID               int64                  `bun:"task_id"`
	TaskStatus           model.ErrandTaskStatus `bun:"task_status"`
	TaskItemID           int64                  `bun:"task_item_id"`
	PurchasedQuantity    *int32                 `bun:"purchased_quantity"`
	ActualUnitPriceCents *int32                 `bun:"actual_unit_price_cents"`
	TaskItemUpdatedAt    time.Time              `bun:"task_item_updated_at"`
}

func GetDistributingTaskHeader(
	ctx context.Context,
	db bun.IDB,
	taskID, captainID int64,
) (*DistributingTaskHeaderRow, error) {
	var row DistributingTaskHeaderRow
	err := db.NewSelect().
		TableExpr("errand.errand_task AS et").
		Join("LEFT JOIN catalog.catalog_store AS cs ON cs.id = et.store_id").
		ColumnExpr("et.id AS task_id").
		ColumnExpr("et.store_id AS store_id").
		ColumnExpr("COALESCE(cs.name, '') AS store_name").
		ColumnExpr("et.packaging_fee_cents AS packaging_fee_cents").
		ColumnExpr("et.status AS status").
		Where("et.id = ?", taskID).
		Where("et.captain_id = ?", captainID).
		Limit(1).
		Scan(ctx, &row)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func ListDistributingTaskDetails(ctx context.Context, db bun.IDB, taskID int64) ([]DistributingTaskDetailRow, error) {
	rows := make([]DistributingTaskDetailRow, 0)
	err := db.NewSelect().
		TableExpr("errand.errand_task_item AS eti").
		Join("JOIN errand.errand_task_assignment AS eta ON eta.task_item_id = eti.id").
		Join("JOIN errand.errand_demand_item AS edi ON edi.id = eta.demand_item_id").
		Join("LEFT JOIN user.user_account AS ua ON ua.id = eta.purchaser_id").
		ColumnExpr("eti.id AS task_item_id").
		ColumnExpr("eti.product_template_id AS product_template_id").
		ColumnExpr("eti.title_snapshot AS title_snapshot").
		ColumnExpr("eti.description_snapshot AS description_snapshot").
		ColumnExpr("eti.image_url_snapshot AS image_url_snapshot").
		ColumnExpr("edi.estimated_unit_price_cents AS origin_unit_price_cents").
		ColumnExpr("eti.actual_unit_price_cents AS actual_unit_price_cents").
		ColumnExpr("eta.purchaser_id AS purchaser_id").
		ColumnExpr("COALESCE(ua.display_name, '') AS purchaser_name").
		ColumnExpr("COALESCE(ua.avatar_url, '') AS purchaser_avatar_url").
		ColumnExpr("edi.quantity AS quantity").
		ColumnExpr("eta.distributed_quantity AS distributed_quantity").
		ColumnExpr("eta.id AS task_assignment_id").
		ColumnExpr("edi.id AS demand_item_id").
		ColumnExpr("eta.updated_at AS task_assignment_updated_at").
		Where("eti.task_id = ?", taskID).
		OrderExpr("eti.deadline ASC, eti.id ASC, eta.id ASC").
		Scan(ctx, &rows)
	return rows, err
}

func GetDistributingTaskItemForUpdate(
	ctx context.Context,
	db bun.IDB,
	taskID, taskItemID, captainID int64,
) (*DistributingTaskItemForUpdateRow, error) {
	var row DistributingTaskItemForUpdateRow
	err := db.NewSelect().
		TableExpr("errand.errand_task_item AS eti").
		Join("JOIN errand.errand_task AS et ON et.id = eti.task_id").
		ColumnExpr("et.id AS task_id").
		ColumnExpr("et.status AS task_status").
		ColumnExpr("eti.id AS task_item_id").
		ColumnExpr("eti.purchased_quantity AS purchased_quantity").
		ColumnExpr("eti.actual_unit_price_cents AS actual_unit_price_cents").
		ColumnExpr("eti.updated_at AS task_item_updated_at").
		Where("et.id = ?", taskID).
		Where("eti.id = ?", taskItemID).
		Where("et.captain_id = ?", captainID).
		Limit(1).
		For("UPDATE").
		Scan(ctx, &row)
	if err != nil {
		return nil, err
	}
	return &row, nil
}
// - INSERT errand_price_change_log：记录 old_unit_price_cents 和 new_unit_price_cents
func CreatePriceChangeLog(ctx context.Context, db bun.IDB, priceChangeLog *model.ErrandPriceChangeLog) error {
	_, err := db.NewInsert().Model(priceChangeLog).Exec(ctx)
	return err
}
//- UPDATE errand_task_item.actual_unit_price_cents：基于 errand_task_item_updated_at 校验并发后更新实际采购单价
func UpdateTaskItemActualPrice(
	ctx context.Context,
	db bun.IDB,
	taskItemID int64,
	expectedUpdatedAt time.Time,
	actualUnitPriceCents int32,
	now time.Time,
) error {
	res, err := db.NewUpdate().
		Model((*model.ErrandTaskItem)(nil)).
		Set("actual_unit_price_cents = ?", actualUnitPriceCents).
		Set("updated_at = ?", now).
		Where("id = ?", taskItemID).
		Where("updated_at = ?", expectedUpdatedAt).
		Exec(ctx)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

type DistributingTaskAssignmentForUpdateRow struct {
	TaskID              int64                  `bun:"task_id"`
	TaskStatus          model.ErrandTaskStatus `bun:"task_status"`
	TaskItemID          int64                  `bun:"task_item_id"`
	AssignmentID        int64                  `bun:"assignment_id"`
	PurchasedQuantity   *int32                 `bun:"purchased_quantity"`
	DemandQuantity      int32                  `bun:"demand_quantity"`
	DistributedQuantity int32                  `bun:"distributed_quantity"`
	AssignmentUpdatedAt time.Time              `bun:"assignment_updated_at"`
}

func UpdateTaskToDistributing(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
	expectedUpdatedAt time.Time,
	packagingFeeCents int32,
	now time.Time,
) error {
	res, err := db.NewUpdate().
		Model((*model.ErrandTask)(nil)).
		Set("status = ?", model.ErrandTaskStatusDistributing).
		Set("packaging_fee_cents = ?", packagingFeeCents).
		Set("updated_at = ?", now).
		Where("id = ?", taskID).
		Where("status = ?", model.ErrandTaskStatusPendingDistributing).
		Where("updated_at = ?", expectedUpdatedAt).
		Exec(ctx)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func UpdateTaskRelatedDemandsToDistributing(ctx context.Context, db bun.IDB, taskID int64, now time.Time) error {
	_, err := db.NewUpdate().
		Model((*model.ErrandDemand)(nil)).
		Set("status = ?", model.ErrandDemandStatusDistributing).
		Set("updated_at = ?", now).
		Where(`id IN (
			SELECT DISTINCT edi.errand_demand_id
			FROM errand.errand_task_assignment AS eta
			JOIN errand.errand_demand_item AS edi ON edi.id = eta.demand_item_id
			WHERE eta.task_id = ?
		)`, taskID).
		Where("status = ?", model.ErrandDemandStatusPendingDistributing).
		Exec(ctx)
	return err
}

func UpdateTaskRelatedDemandItemsToDistributing(ctx context.Context, db bun.IDB, taskID int64, now time.Time) error {
	_, err := db.NewUpdate().
		Model((*model.ErrandDemandItem)(nil)).
		Set("status = ?", model.ErrandDemandItemStatusDistributing).
		Set("updated_at = ?", now).
		Where(`id IN (
			SELECT eta.demand_item_id
			FROM errand.errand_task_assignment AS eta
			WHERE eta.task_id = ?
		)`, taskID).
		Where("status = ?", model.ErrandDemandItemStatusPendingDistributing).
		Exec(ctx)
	return err
}

func GetDistributingTaskAssignmentForUpdate(
	ctx context.Context,
	db bun.IDB,
	taskItemID, assignmentID, captainID int64,
) (*DistributingTaskAssignmentForUpdateRow, error) {
	var row DistributingTaskAssignmentForUpdateRow
	err := db.NewSelect().
		TableExpr("errand.errand_task_assignment AS eta").
		Join("JOIN errand.errand_task_item AS eti ON eti.id = eta.task_item_id AND eti.task_id = eta.task_id").
		Join("JOIN errand.errand_task AS et ON et.id = eta.task_id").
		Join("JOIN errand.errand_demand_item AS edi ON edi.id = eta.demand_item_id").
		ColumnExpr("et.id AS task_id").
		ColumnExpr("et.status AS task_status").
		ColumnExpr("eti.id AS task_item_id").
		ColumnExpr("eta.id AS assignment_id").
		ColumnExpr("eti.purchased_quantity AS purchased_quantity").
		ColumnExpr("edi.quantity AS demand_quantity").
		ColumnExpr("eta.distributed_quantity AS distributed_quantity").
		ColumnExpr("eta.updated_at AS assignment_updated_at").
		Where("eti.id = ?", taskItemID).
		Where("eta.id = ?", assignmentID).
		Where("et.captain_id = ?", captainID).
		Limit(1).
		For("UPDATE").
		Scan(ctx, &row)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func SumTaskItemDistributedQuantity(ctx context.Context, db bun.IDB, taskItemID int64) (int64, error) {
	var row struct {
		TotalDistributed int64 `bun:"total_distributed"`
	}
	err := db.NewSelect().
		TableExpr("errand.errand_task_assignment AS eta").
		ColumnExpr("COALESCE(SUM(eta.distributed_quantity), 0) AS total_distributed").
		Where("eta.task_item_id = ?", taskItemID).
		Scan(ctx, &row)
	return row.TotalDistributed, err
}

func UpdateDistributingTaskAssignment(
	ctx context.Context,
	db bun.IDB,
	assignmentID int64,
	expectedUpdatedAt time.Time,
	distributedQuantity int32,
	now time.Time,
) error {
	res, err := db.NewUpdate().
		Model((*model.ErrandTaskAssignment)(nil)).
		Set("distributed_quantity = ?", distributedQuantity).
		Set("updated_at = ?", now).
		Where("id = ?", assignmentID).
		Where("updated_at = ?", expectedUpdatedAt).
		Exec(ctx)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

type TaskDistributionSummaryRow struct {
	TotalTaskItemCount int64 `bun:"total_task_item_count"`
	UnhandledCount     int64 `bun:"unhandled_count"`
	UnpricedCount      int64 `bun:"unpriced_count"`
	IncompleteCount    int64 `bun:"incomplete_count"`
}
// 计算task在待收款 阶段前 的分配完成度
func GetTaskDistributionSummary(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
) (*TaskDistributionSummaryRow, error) {
	var row TaskDistributionSummaryRow
	err := db.NewSelect().
		TableExpr("errand.errand_task_item AS eti").
		// 左联子查询，从errand_task_assignment查询task_item_id和distributed_quantity，
		// 对 distributed_quantity 求和，如果 SUM 结果是 NULL，返回 0；否则返回 SUM 值
		Join(`LEFT JOIN (
			SELECT task_item_id, COALESCE(SUM(distributed_quantity), 0) AS distributed_quantity
			FROM errand.errand_task_assignment
			WHERE task_id = ?
			GROUP BY task_item_id
		) AS distribution ON distribution.task_item_id = eti.id`, taskID).
		// 查询task_item总数
		ColumnExpr("COUNT(*) AS total_task_item_count").
		// 查询未采购数 
		ColumnExpr("COUNT(*) FILTER (WHERE eti.purchased_quantity IS NULL) AS unhandled_count").
		// 查询未定价数
		ColumnExpr("COUNT(*) FILTER (WHERE eti.actual_unit_price_cents IS NULL) AS unpriced_count").
		// 查询分配未完成数 <> 不等于，查询已经采购的并且已分配数量不等于采购数量的
		ColumnExpr(`
			COUNT(*) FILTER (
				WHERE eti.purchased_quantity IS NOT NULL
				AND COALESCE(distribution.distributed_quantity, 0) <> eti.purchased_quantity
			) AS incomplete_count
		`).
		Where("eti.task_id = ?", taskID).
		Scan(ctx, &row)
	return &row, err
}
// 更新task主表
func UpdateTaskToCollectingPayment(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
	expectedUpdatedAt time.Time,
	now time.Time,
) error {
	res, err := db.NewUpdate().
		Model((*model.ErrandTask)(nil)).
		Set("status = ?", model.ErrandTaskStatusCollectingPayment).
		Set("distribution_completed_at = ?", now).
		Set("updated_at = ?", now).
		Where("id = ?", taskID).
		Where("status = ?", model.ErrandTaskStatusDistributing).
		Where("updated_at = ?", expectedUpdatedAt).
		Exec(ctx)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
// 更新task对应的demand表状态
func UpdateTaskRelatedDemandsToPendingPayment(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
	now time.Time,
) error {
	_, err := db.NewUpdate().
		Model((*model.ErrandDemand)(nil)).
		Set("status = ?", model.ErrandDemandStatusPendingPayment).
		Set("updated_at = ?", now).
		Where(`id IN (
			SELECT DISTINCT edi.errand_demand_id
			FROM errand.errand_task_assignment AS eta
			JOIN errand.errand_demand_item AS edi ON edi.id = eta.demand_item_id
			WHERE eta.task_id = ?
		)`, taskID).
		Where("status = ?", model.ErrandDemandStatusDistributing).
		Exec(ctx)
	return err
}
// 更新task对应的demand_item表状态
func UpdateTaskRelatedDemandItemsToPendingPayment(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
	now time.Time,
) error {
	_, err := db.NewUpdate().
		Model((*model.ErrandDemandItem)(nil)).
		Set("status = ?", model.ErrandDemandItemStatusPendingPayment).
		Set("updated_at = ?", now).
		Where(`id IN (
			SELECT eta.demand_item_id
			FROM errand.errand_task_assignment AS eta
			WHERE eta.task_id = ?
		)`, taskID).
		Where("status = ?", model.ErrandDemandItemStatusDistributing).
		Exec(ctx)
	return err
}

type CollectingPaymentTaskHeaderRow struct {
	TaskID            int64                  `bun:"task_id"`
	CaptainID         int64                  `bun:"captain_id"`
	CaptainName       string                 `bun:"captain_name"`
	CaptainAvatarURL  string                 `bun:"captain_avatar_url"`
	PackagingFeeCents int32                  `bun:"packaging_fee_cents"`
	Status            model.ErrandTaskStatus `bun:"status"`
}

func GetCollectingPaymentTaskHeader(
	ctx context.Context,
	db bun.IDB,
	taskID, captainID int64,
) (*CollectingPaymentTaskHeaderRow, error) {
	var row CollectingPaymentTaskHeaderRow
	err := db.NewSelect().
		TableExpr("errand.errand_task AS et").
		Join(`LEFT JOIN "user".user_account AS ua ON ua.id = et.captain_id`).
		ColumnExpr("et.id AS task_id").
		ColumnExpr("et.captain_id AS captain_id").
		ColumnExpr("COALESCE(ua.display_name, '') AS captain_name").
		ColumnExpr("COALESCE(ua.avatar_url, '') AS captain_avatar_url").
		ColumnExpr("et.packaging_fee_cents AS packaging_fee_cents").
		ColumnExpr("et.status AS status").
		Where("et.id = ?", taskID).
		Where("et.captain_id = ?", captainID).
		Limit(1).
		Scan(ctx, &row)
	return &row, err
}

type CollectingPaymentDetailRow struct {
	TaskItemID             int64      `bun:"task_item_id"`
	DemandItemID           int64      `bun:"demand_item_id"`
	PayerID                int64      `bun:"payer_id"`
	PayerName              string     `bun:"payer_name"`
	PayerAvatarURL         string     `bun:"payer_avatar_url"`
	PayeeID                int64      `bun:"payee_id"`
	PayeeName              string     `bun:"payee_name"`
	PayeeAvatarURL         string     `bun:"payee_avatar_url"`
	TitleSnapshot          string     `bun:"title_snapshot"`
	RequiredQuantity       int32      `bun:"required_quantity"`
	PurchasedQuantity      int32      `bun:"purchased_quantity"`
	DistributedQuantity    int32      `bun:"distributed_quantity"`
	ActualUnitPriceCents   int32      `bun:"actual_unit_price_cents"`
	ServiceFeePerUnitCents int32      `bun:"service_fee_per_unit_cents"`
	NonPurchaseReason      string     `bun:"non_purchase_reason"`
	PaymentBillID          *int64     `bun:"payment_bill_id"`
	BillNo                 string     `bun:"bill_no"`
	BillStatus             string     `bun:"bill_status"`
	BillAmountCents        *int32     `bun:"bill_amount_cents"`
	VerifyCode             string     `bun:"verify_code"`
	PaymentChannel         *string    `bun:"payment_channel"`
	SerialNumber           *string    `bun:"serial_number"`
	SubmittedAt            *time.Time `bun:"submitted_at"`
	CompletedAt            *time.Time `bun:"completed_at"`
	ClosedAt               *time.Time `bun:"closed_at"`
	BillCreatedAt          *time.Time `bun:"bill_created_at"`
	BillUpdatedAt          *time.Time `bun:"bill_updated_at"`
	SourceType             *string    `bun:"source_type"`
	SourceID               *int64     `bun:"source_id"`
}

func ListCollectingPaymentDetails(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
) ([]CollectingPaymentDetailRow, error) {
	rows := make([]CollectingPaymentDetailRow, 0)
	err := db.NewSelect().
		TableExpr("errand.errand_task_item AS eti").
		Join("JOIN errand.errand_task_assignment AS eta ON eta.task_item_id = eti.id AND eta.task_id = eti.task_id").
		Join("JOIN errand.errand_demand_item AS edi ON edi.id = eta.demand_item_id").
		Join("JOIN errand.errand_task AS et ON et.id = eti.task_id").
		Join(`LEFT JOIN "user".user_account AS payer ON payer.id = eta.purchaser_id`).
		Join(`LEFT JOIN "user".user_account AS payee ON payee.id = et.captain_id`).
		Join("LEFT JOIN payment.payment_bill AS pb ON pb.id = eta.payment_bill_id").
		ColumnExpr("eti.id AS task_item_id").
		ColumnExpr("edi.id AS demand_item_id").
		ColumnExpr("eta.purchaser_id AS payer_id").
		ColumnExpr("COALESCE(payer.display_name, '') AS payer_name").
		ColumnExpr("COALESCE(payer.avatar_url, '') AS payer_avatar_url").
		ColumnExpr("et.captain_id AS payee_id").
		ColumnExpr("COALESCE(payee.display_name, '') AS payee_name").
		ColumnExpr("COALESCE(payee.avatar_url, '') AS payee_avatar_url").
		ColumnExpr("eti.title_snapshot AS title_snapshot").
		ColumnExpr("edi.quantity AS required_quantity").
		ColumnExpr("COALESCE(eti.purchased_quantity, 0) AS purchased_quantity").
		ColumnExpr("eta.distributed_quantity AS distributed_quantity").
		ColumnExpr("COALESCE(eti.actual_unit_price_cents, 0) AS actual_unit_price_cents").
		ColumnExpr("eta.service_fee_per_unit_cents AS service_fee_per_unit_cents").
		ColumnExpr("eti.non_purchase_reason AS non_purchase_reason").
		ColumnExpr("eta.payment_bill_id AS payment_bill_id").
		ColumnExpr("COALESCE(pb.bill_no, '') AS bill_no").
		ColumnExpr("COALESCE(pb.status::text, '') AS bill_status").
		ColumnExpr("pb.amount_cents AS bill_amount_cents").
		ColumnExpr("COALESCE(pb.verify_code, '') AS verify_code").
		ColumnExpr("pb.channel::text AS payment_channel").
		ColumnExpr("pb.serial_number AS serial_number").
		ColumnExpr("pb.submitted_at AS submitted_at").
		ColumnExpr("pb.completed_at AS completed_at").
		ColumnExpr("pb.closed_at AS closed_at").
		ColumnExpr("pb.created_at AS bill_created_at").
		ColumnExpr("pb.updated_at AS bill_updated_at").
		ColumnExpr("pb.source_type AS source_type").
		ColumnExpr("pb.source_id AS source_id").
		Where("eti.task_id = ?", taskID).
		OrderExpr("eti.deadline ASC, eti.id ASC, eta.id ASC").
		Scan(ctx, &rows)
	return rows, err
}

type TaskPaymentSummaryRow struct {
	PayerCount          int64 `bun:"payer_count"`
	IncompleteBillCount int64 `bun:"incomplete_bill_count"`
}

type TaskPaymentBillAssignmentRow struct {
	AssignmentID           int64 `bun:"assignment_id"`
	PayerID                int64 `bun:"payer_id"`
	PayeeID                int64 `bun:"payee_id"`
	ActualUnitPriceCents   int32 `bun:"actual_unit_price_cents"`
	DistributedQuantity    int32 `bun:"distributed_quantity"`
	ServiceFeePerUnitCents int32 `bun:"service_fee_per_unit_cents"`
	PackagingFeeCents      int32 `bun:"packaging_fee_cents"`
}

func GetTaskPaymentSummary(ctx context.Context, db bun.IDB, taskID int64) (*TaskPaymentSummaryRow, error) {
	var row TaskPaymentSummaryRow
	err := db.NewSelect().
		TableExpr("errand.errand_task_assignment AS eta").
		Join("LEFT JOIN payment.payment_bill AS pb ON pb.id = eta.payment_bill_id").
		ColumnExpr("COUNT(DISTINCT eta.purchaser_id) AS payer_count").
		ColumnExpr(`
			COUNT(DISTINCT eta.purchaser_id) FILTER (
				WHERE eta.payment_bill_id IS NULL OR COALESCE(pb.status::text, '') <> 'completed'
			) AS incomplete_bill_count
		`).
		Where("eta.task_id = ?", taskID).
		Scan(ctx, &row)
	return &row, err
}

func ListTaskPaymentBillAssignments(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
) ([]TaskPaymentBillAssignmentRow, error) {
	rows := make([]TaskPaymentBillAssignmentRow, 0)
	err := db.NewSelect().
		TableExpr("errand.errand_task_assignment AS eta").
		Join("JOIN errand.errand_task_item AS eti ON eti.id = eta.task_item_id AND eti.task_id = eta.task_id").
		Join("JOIN errand.errand_task AS et ON et.id = eta.task_id").
		ColumnExpr("eta.id AS assignment_id").
		ColumnExpr("eta.purchaser_id AS payer_id").
		ColumnExpr("et.captain_id AS payee_id").
		ColumnExpr("COALESCE(eti.actual_unit_price_cents, 0) AS actual_unit_price_cents").
		ColumnExpr("eta.distributed_quantity AS distributed_quantity").
		ColumnExpr("eta.service_fee_per_unit_cents AS service_fee_per_unit_cents").
		ColumnExpr("et.packaging_fee_cents AS packaging_fee_cents").
		Where("eta.task_id = ?", taskID).
		OrderExpr("eta.purchaser_id ASC, eta.id ASC").
		Scan(ctx, &rows)
	return rows, err
}

func UpdateTaskAssignmentPaymentBillIDByPayer(
	ctx context.Context,
	db bun.IDB,
	taskID, payerID, billID int64,
	now time.Time,
) error {
	res, err := db.NewUpdate().
		Model((*model.ErrandTaskAssignment)(nil)).
		Set("payment_bill_id = ?", billID).
		Set("updated_at = ?", now).
		Where("task_id = ?", taskID).
		Where("purchaser_id = ?", payerID).
		Exec(ctx)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func UpdateTaskDemandItemsToCompletedByPayer(
	ctx context.Context,
	db bun.IDB,
	taskID, payerID int64,
	now time.Time,
) error {
	_, err := db.NewUpdate().
		Model((*model.ErrandDemandItem)(nil)).
		Set("status = ?", model.ErrandDemandItemStatusCompleted).
		Set("updated_at = ?", now).
		Where(`id IN (
			SELECT eta.demand_item_id
			FROM errand.errand_task_assignment AS eta
			WHERE eta.task_id = ? AND eta.purchaser_id = ?
		)`, taskID, payerID).
		Where("status = ?", model.ErrandDemandItemStatusPendingPayment).
		Exec(ctx)
	return err
}

func UpdateTaskToCompleted(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
	expectedUpdatedAt time.Time,
	now time.Time,
) error {
	res, err := db.NewUpdate().
		Model((*model.ErrandTask)(nil)).
		Set("status = ?", model.ErrandTaskStatusCompleted).
		Set("payment_completed_at = ?", now).
		Set("updated_at = ?", now).
		Where("id = ?", taskID).
		Where("status = ?", model.ErrandTaskStatusCollectingPayment).
		Where("updated_at = ?", expectedUpdatedAt).
		Exec(ctx)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func UpdateTaskToCompletedWithoutUpdatedAt(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
	now time.Time,
) error {
	res, err := db.NewUpdate().
		Model((*model.ErrandTask)(nil)).
		Set("status = ?", model.ErrandTaskStatusCompleted).
		Set("payment_completed_at = ?", now).
		Set("updated_at = ?", now).
		Where("id = ?", taskID).
		Where("status = ?", model.ErrandTaskStatusCollectingPayment).
		Exec(ctx)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func UpdateTaskRelatedDemandsToCompleted(ctx context.Context, db bun.IDB, taskID int64, now time.Time) error {
	_, err := db.NewUpdate().
		Model((*model.ErrandDemand)(nil)).
		Set("status = ?", model.ErrandDemandStatusCompleted).
		Set("payment_completed_at = ?", now).
		Set("updated_at = ?", now).
		Where(`id IN (
			SELECT DISTINCT edi.errand_demand_id
			FROM errand.errand_task_assignment AS eta
			JOIN errand.errand_demand_item AS edi ON edi.id = eta.demand_item_id
			WHERE eta.task_id = ?
		)`, taskID).
		Where("status = ?", model.ErrandDemandStatusPendingPayment).
		Exec(ctx)
	return err
}

func UpdateTaskRelatedDemandItemsToCompleted(ctx context.Context, db bun.IDB, taskID int64, now time.Time) error {
	_, err := db.NewUpdate().
		Model((*model.ErrandDemandItem)(nil)).
		Set("status = ?", model.ErrandDemandItemStatusCompleted).
		Set("updated_at = ?", now).
		Where(`id IN (
			SELECT eta.demand_item_id
			FROM errand.errand_task_assignment AS eta
			WHERE eta.task_id = ?
		)`, taskID).
		Where("status = ?", model.ErrandDemandItemStatusPendingPayment).
		Exec(ctx)
	return err
}

type ErrandTaskListRow struct {
	TaskID    int64                  `bun:"task_id"`
	StoreID   int64                  `bun:"store_id"`
	StoreName string                 `bun:"store_name"`
	Status    model.ErrandTaskStatus `bun:"status"`
	CreatedAt time.Time              `bun:"created_at"`
}

type ErrandTaskListItemRow struct {
	TaskID               int64     `bun:"task_id"`
	TaskItemID           int64     `bun:"task_item_id"`
	ProductTemplateID    int64     `bun:"product_template_id"`
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

func CountErrandTasks(
	ctx context.Context,
	db bun.IDB,
	captainID int64,
	status *model.ErrandTaskStatus,
) (int32, error) {
	var row struct {
		Count int32 `bun:"count"`
	}
	q := db.NewSelect().
		TableExpr("errand.errand_task AS et").
		ColumnExpr("COUNT(*) AS count").
		Where("et.captain_id = ?", captainID)
	if status != nil {
		q = q.Where("et.status = ?", *status)
	}
	err := q.Scan(ctx, &row)
	return row.Count, err
}

func ListErrandTasks(
	ctx context.Context,
	db bun.IDB,
	captainID int64,
	status *model.ErrandTaskStatus,
	limit int,
	offset int,
) ([]ErrandTaskListRow, error) {
	rows := make([]ErrandTaskListRow, 0)
	q := db.NewSelect().
		TableExpr("errand.errand_task AS et").
		Join("LEFT JOIN catalog.catalog_store AS cs ON cs.id = et.store_id").
		ColumnExpr("et.id AS task_id").
		ColumnExpr("et.store_id AS store_id").
		ColumnExpr("COALESCE(cs.name, '') AS store_name").
		ColumnExpr("et.status AS status").
		ColumnExpr("et.created_at AS created_at").
		Where("et.captain_id = ?", captainID).
		OrderExpr("et.created_at DESC, et.id DESC").
		Limit(limit).
		Offset(offset)
	if status != nil {
		q = q.Where("et.status = ?", *status)
	}
	err := q.Scan(ctx, &rows)
	return rows, err
}

func ListErrandTaskItems(
	ctx context.Context,
	db bun.IDB,
	taskIDs []int64,
) ([]ErrandTaskListItemRow, error) {
	if len(taskIDs) == 0 {
		return []ErrandTaskListItemRow{}, nil
	}

	rows := make([]ErrandTaskListItemRow, 0)
	err := db.NewSelect().
		TableExpr("errand.errand_task_item AS eti").
		Join("LEFT JOIN catalog.catalog_product_template AS cpt ON cpt.id = eti.product_template_id").
		ColumnExpr("eti.task_id AS task_id").
		ColumnExpr("eti.id AS task_item_id").
		ColumnExpr("eti.product_template_id AS product_template_id").
		ColumnExpr("eti.title_snapshot AS title_snapshot").
		ColumnExpr("eti.description_snapshot AS description_snapshot").
		ColumnExpr("eti.image_url_snapshot AS image_url_snapshot").
		ColumnExpr("COALESCE(cpt.price_cents, 0) AS product_price_cents").
		ColumnExpr("eti.required_quantity AS required_quantity").
		ColumnExpr("eti.purchased_quantity AS purchased_quantity").
		ColumnExpr("eti.non_purchase_reason AS non_purchase_reason").
		ColumnExpr("eti.actual_unit_price_cents AS actual_unit_price_cents").
		ColumnExpr("eti.updated_at AS updated_at").
		Where("eti.task_id IN (?)", bun.List(taskIDs)).
		OrderExpr("eti.task_id ASC, eti.deadline ASC, eti.id ASC").
		Scan(ctx, &rows)
	return rows, err
}

func UpdateTaskToCancelled(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
	expectedUpdatedAt time.Time,
	now time.Time,
) error {
	res, err := db.NewUpdate().
		Model((*model.ErrandTask)(nil)).
		Set("status = ?", model.ErrandTaskStatusCancelled).
		Set("cancelled_at = ?", now).
		Set("updated_at = ?", now).
		Where("id = ?", taskID).
		Where("updated_at = ?", expectedUpdatedAt).
		Where("status NOT IN (?)", bun.List([]model.ErrandTaskStatus{
			model.ErrandTaskStatusCompleted,
			model.ErrandTaskStatusCancelled,
		})).
		Exec(ctx)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func UpdateTaskRelatedDemandsToCancelled(ctx context.Context, db bun.IDB, taskID int64, now time.Time) error {
	_, err := db.NewUpdate().
		Model((*model.ErrandDemand)(nil)).
		Set("status = ?", model.ErrandDemandStatusCancelled).
		Set("cancelled_at = ?", now).
		Set("updated_at = ?", now).
		Where(`id IN (
			SELECT DISTINCT edi.errand_demand_id
			FROM errand.errand_task_assignment AS eta
			JOIN errand.errand_demand_item AS edi ON edi.id = eta.demand_item_id
			WHERE eta.task_id = ?
		)`, taskID).
		Where("status NOT IN (?)", bun.List([]model.ErrandDemandStatus{
			model.ErrandDemandStatusCompleted,
			model.ErrandDemandStatusCancelled,
		})).
		Exec(ctx)
	return err
}

func UpdateTaskRelatedDemandItemsToCancelled(ctx context.Context, db bun.IDB, taskID int64, now time.Time) error {
	_, err := db.NewUpdate().
		Model((*model.ErrandDemandItem)(nil)).
		Set("status = ?", model.ErrandDemandItemStatusCancelled).
		Set("updated_at = ?", now).
		Where(`id IN (
			SELECT eta.demand_item_id
			FROM errand.errand_task_assignment AS eta
			WHERE eta.task_id = ?
		)`, taskID).
		Where("status NOT IN (?)", bun.List([]model.ErrandDemandItemStatus{
			model.ErrandDemandItemStatusCompleted,
			model.ErrandDemandItemStatusCancelled,
		})).
		Exec(ctx)
	return err
}
