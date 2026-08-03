package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/uptrace/bun"
)

type TaskDistributionSummaryRow struct {
	TotalTaskItemCount int64 `bun:"total_task_item_count"`
	UnhandledCount     int64 `bun:"unhandled_count"`
	UnpricedCount      int64 `bun:"unpriced_count"`
	UnassignedCount    int64 `bun:"unassigned_count"`
	IncompleteCount    int64 `bun:"incomplete_count"`
}

func GetTaskDistributionSummary(ctx context.Context, db bun.IDB, taskID int64) (*TaskDistributionSummaryRow, error) {
	var row TaskDistributionSummaryRow
	err := db.NewSelect().
		TableExpr("errand.errand_task_item AS eti").
		Join(`LEFT JOIN (
			SELECT task_item_id,
				COALESCE(SUM(distributed_quantity), 0) AS total_distributed,
				COUNT(*) FILTER (WHERE distributed_quantity IS NULL) AS unassigned_count
			FROM errand.errand_task_assignment
			GROUP BY task_item_id
		) AS eta_sum ON eta_sum.task_item_id = eti.id`).
		ColumnExpr("COUNT(*) AS total_task_item_count").
		ColumnExpr("COUNT(*) FILTER (WHERE eti.purchased_quantity IS NULL) AS unhandled_count").
		ColumnExpr(`COUNT(*) FILTER (
			WHERE COALESCE(eti.purchased_quantity, 0) > 0
				AND eti.actual_unit_price_cents IS NULL
		) AS unpriced_count`).
		ColumnExpr(`COUNT(*) FILTER (
			WHERE eti.purchased_quantity IS NOT NULL
				AND COALESCE(eta_sum.unassigned_count, 0) > 0
		) AS unassigned_count`).
		ColumnExpr(`COUNT(*) FILTER (
			WHERE eti.purchased_quantity IS NOT NULL
				AND COALESCE(eta_sum.total_distributed, 0) <> eti.purchased_quantity
		) AS incomplete_count`).
		Where("eti.task_id = ?", taskID).
		Scan(ctx, &row)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func UpdateTaskToCollectingPayment(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
	expectedUpdatedAt time.Time,
	now time.Time,
) (time.Time, error) {
	task := &model.ErrandTask{ID: taskID}
	res, err := db.NewUpdate().
		Model(task).
		Set("status = ?", model.ErrandTaskStatusCollectingPayment).
		Set("distribution_completed_at = ?", now).
		Set("updated_at = ?", now).
		WherePK().
		Where("status = ?", model.ErrandTaskStatusDistributing).
		Where("updated_at = ?", expectedUpdatedAt).
		Returning("updated_at").
		Exec(ctx)
	if err != nil {
		return time.Time{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return time.Time{}, err
	}
	if affected == 0 {
		return time.Time{}, sql.ErrNoRows
	}
	return task.UpdatedAt, nil
}

func UpdateTaskRelatedDemandsToPendingPayment(ctx context.Context, db bun.IDB, taskID int64, now time.Time) error {
	_, err := db.NewUpdate().
		Model((*model.ErrandDemand)(nil)).
		Set("status = ?", model.ErrandDemandStatusPendingPayment).
		Set("distribution_completed_at = ?", now).
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

func UpdateTaskRelatedDemandItemsToPendingPayment(ctx context.Context, db bun.IDB, taskID int64, now time.Time) error {
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

type TaskPaymentBillAssignmentRow struct {
	AssignmentID           int64  `bun:"assignment_id"`
	PayerID                int64  `bun:"payer_id"`
	PayeeID                int64  `bun:"payee_id"`
	PackagingFeeCents      int32  `bun:"packaging_fee_cents"`
	ActualUnitPriceCents   int32  `bun:"actual_unit_price_cents"`
	DistributedQuantity    *int32 `bun:"distributed_quantity"`
	ServiceFeePerUnitCents int32  `bun:"service_fee_per_unit_cents"`
}

func ListTaskPaymentBillAssignments(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
) ([]TaskPaymentBillAssignmentRow, error) {
	rows := make([]TaskPaymentBillAssignmentRow, 0)
	err := db.NewSelect().
		TableExpr("errand.errand_task_assignment AS eta").
		Join("JOIN errand.errand_task AS et ON et.id = eta.task_id").
		Join("JOIN errand.errand_task_item AS eti ON eti.id = eta.task_item_id").
		ColumnExpr("eta.id AS assignment_id").
		ColumnExpr("eta.purchaser_id AS payer_id").
		ColumnExpr("et.captain_id AS payee_id").
		ColumnExpr("et.packaging_fee_cents AS packaging_fee_cents").
		ColumnExpr("COALESCE(eti.actual_unit_price_cents, 0) AS actual_unit_price_cents").
		ColumnExpr("eta.distributed_quantity AS distributed_quantity").
		ColumnExpr("eta.service_fee_per_unit_cents AS service_fee_per_unit_cents").
		Where("eta.task_id = ?", taskID).
		Where("eta.distributed_quantity > 0").
		Where("eta.purchaser_id != et.captain_id").
		OrderExpr("eta.purchaser_id ASC, eta.id ASC").
		Scan(ctx, &rows)
	return rows, err
}

func UpdateTaskAssignmentPaymentBillIDByPayer(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
	payerID int64,
	paymentBillID int64,
	now time.Time,
) error {
	res, err := db.NewUpdate().
		Model((*model.ErrandTaskAssignment)(nil)).
		Set("payment_bill_id = ?", paymentBillID).
		Set("updated_at = ?", now).
		Where("task_id = ?", taskID).
		Where("purchaser_id = ?", payerID).
		Where("distributed_quantity > 0").
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

type CollectingPaymentTaskHeaderRow struct {
	TaskID            int64                  `bun:"task_id"`
	PackagingFeeCents int32                  `bun:"packaging_fee_cents"`
	Status            model.ErrandTaskStatus `bun:"status"`
	UpdatedAt         time.Time              `bun:"updated_at"`
}

func GetCollectingPaymentTaskHeader(
	ctx context.Context,
	db bun.IDB,
	taskID, captainID int64,
) (*CollectingPaymentTaskHeaderRow, error) {
	var row CollectingPaymentTaskHeaderRow
	err := db.NewSelect().
		TableExpr("errand.errand_task AS et").
		ColumnExpr("et.id AS task_id").
		ColumnExpr("et.packaging_fee_cents AS packaging_fee_cents").
		ColumnExpr("et.status AS status").
		ColumnExpr("et.updated_at AS updated_at").
		Where("et.id = ?", taskID).
		Where("et.captain_id = ?", captainID).
		Limit(1).
		Scan(ctx, &row)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

type CollectingPaymentDetailRow struct {
	DemandItemID           int64  `bun:"demand_item_id"`
	TitleSnapshot          string `bun:"title_snapshot"`
	RequiredQuantity       int32  `bun:"required_quantity"`
	PurchasedQuantity      int32  `bun:"purchased_quantity"`
	DistributedQuantity    *int32 `bun:"distributed_quantity"`
	ActualUnitPriceCents   int32  `bun:"actual_unit_price_cents"`
	ServiceFeePerUnitCents int32  `bun:"service_fee_per_unit_cents"`
	NonPurchaseReason      string `bun:"non_purchase_reason"`
	PaymentBillID          *int64 `bun:"payment_bill_id"`
	PayerID                int64  `bun:"payer_id"`
	PayerName              string `bun:"payer_name"`
	PayerAvatarURL         string `bun:"payer_avatar_url"`
	PayeeID                int64  `bun:"payee_id"`
	PayeeName              string `bun:"payee_name"`
	PayeeAvatarURL         string `bun:"payee_avatar_url"`
}

func ListCollectingPaymentDetails(ctx context.Context, db bun.IDB, taskID int64) ([]CollectingPaymentDetailRow, error) {
	rows := make([]CollectingPaymentDetailRow, 0)
	err := db.NewSelect().
		TableExpr("errand.errand_task_assignment AS eta").
		Join("JOIN errand.errand_task AS et ON et.id = eta.task_id").
		Join("JOIN errand.errand_task_item AS eti ON eti.id = eta.task_item_id").
		Join("JOIN errand.errand_demand_item AS edi ON edi.id = eta.demand_item_id").
		Join(`LEFT JOIN "user".user_account AS payer ON payer.id = eta.purchaser_id`).
		Join(`LEFT JOIN "user".user_account AS payee ON payee.id = et.captain_id`).
		ColumnExpr("edi.id AS demand_item_id").
		ColumnExpr("eti.title_snapshot AS title_snapshot").
		ColumnExpr("edi.quantity AS required_quantity").
		ColumnExpr("COALESCE(eti.purchased_quantity, 0) AS purchased_quantity").
		ColumnExpr("eta.distributed_quantity AS distributed_quantity").
		ColumnExpr("COALESCE(eti.actual_unit_price_cents, 0) AS actual_unit_price_cents").
		ColumnExpr("eta.service_fee_per_unit_cents AS service_fee_per_unit_cents").
		ColumnExpr("eti.non_purchase_reason AS non_purchase_reason").
		ColumnExpr("eta.payment_bill_id AS payment_bill_id").
		ColumnExpr("eta.purchaser_id AS payer_id").
		ColumnExpr("COALESCE(payer.display_name, '') AS payer_name").
		ColumnExpr("COALESCE(payer.avatar_url, '') AS payer_avatar_url").
		ColumnExpr("et.captain_id AS payee_id").
		ColumnExpr("COALESCE(payee.display_name, '') AS payee_name").
		ColumnExpr("COALESCE(payee.avatar_url, '') AS payee_avatar_url").
		Where("eta.task_id = ?", taskID).
		Where("eta.distributed_quantity > 0").
		Where("eta.purchaser_id != et.captain_id").
		OrderExpr("eta.purchaser_id ASC, eti.deadline ASC, eti.id ASC, eta.id ASC").
		Scan(ctx, &rows)
	return rows, err
}

func UpdateTaskDemandItemsToCompletedByPayer(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
	payerID int64,
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
	if err != nil {
		return err
	}

	_, err = db.NewUpdate().
		Model((*model.ErrandDemand)(nil)).
		Set("status = ?", model.ErrandDemandStatusCompleted).
		Set("payment_completed_at = ?", now).
		Set("updated_at = ?", now).
		Where(`id IN (
			SELECT DISTINCT edi.errand_demand_id
			FROM errand.errand_task_assignment AS eta
			JOIN errand.errand_demand_item AS edi ON edi.id = eta.demand_item_id
			WHERE eta.task_id = ? AND eta.purchaser_id = ?
		)`, taskID, payerID).
		Where(`NOT EXISTS (
			SELECT 1
			FROM errand.errand_demand_item AS edi_pending
			WHERE edi_pending.errand_demand_id = ed.id
				AND edi_pending.status <> ?
		)`, model.ErrandDemandItemStatusCompleted).
		Exec(ctx)
	return err
}

func GetErrandTaskForUpdateByID(ctx context.Context, db bun.IDB, taskID int64) (*ErrandTaskForUpdateRow, error) {
	var row ErrandTaskForUpdateRow
	err := db.NewSelect().
		TableExpr("errand.errand_task AS et").
		ColumnExpr("et.id AS task_id").
		ColumnExpr("et.captain_id AS captain_id").
		ColumnExpr("et.status AS status").
		ColumnExpr("et.updated_at AS updated_at").
		Where("et.id = ?", taskID).
		Limit(1).
		For("UPDATE").
		Scan(ctx, &row)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// UpdateTaskFromDistributingToCompleted 全自购场景：直接从分发中流转到已完成
func UpdateTaskFromDistributingToCompleted(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
	expectedUpdatedAt time.Time,
	now time.Time,
) (time.Time, error) {
	task := &model.ErrandTask{ID: taskID}
	res, err := db.NewUpdate().
		Model(task).
		Set("status = ?", model.ErrandTaskStatusCompleted).
		Set("distribution_completed_at = ?", now).
		Set("updated_at = ?", now).
		WherePK().
		Where("status = ?", model.ErrandTaskStatusDistributing).
		Where("updated_at = ?", expectedUpdatedAt).
		Returning("updated_at").
		Exec(ctx)
	if err != nil {
		return time.Time{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return time.Time{}, err
	}
	if affected == 0 {
		return time.Time{}, sql.ErrNoRows
	}
	return task.UpdatedAt, nil
}

func UpdateTaskToCompletedWithoutUpdatedAt(ctx context.Context, db bun.IDB, taskID int64, now time.Time) error {
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

func UpdateTaskToCompleted(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
	expectedUpdatedAt time.Time,
	now time.Time,
) (time.Time, error) {
	task := &model.ErrandTask{ID: taskID}
	res, err := db.NewUpdate().
		Model(task).
		Set("status = ?", model.ErrandTaskStatusCompleted).
		Set("payment_completed_at = ?", now).
		Set("updated_at = ?", now).
		WherePK().
		Where("status = ?", model.ErrandTaskStatusCollectingPayment).
		Where("updated_at = ?", expectedUpdatedAt).
		Returning("updated_at").
		Exec(ctx)
	if err != nil {
		return time.Time{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return time.Time{}, err
	}
	if affected == 0 {
		return time.Time{}, sql.ErrNoRows
	}
	return task.UpdatedAt, nil
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
		Where("status IN (?)", bun.List([]model.ErrandDemandStatus{
			model.ErrandDemandStatusPendingPayment,
			model.ErrandDemandStatusCompleted,
		})).
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
		Where("status IN (?)", bun.List([]model.ErrandDemandItemStatus{
			model.ErrandDemandItemStatusPendingPayment,
			model.ErrandDemandItemStatusCompleted,
		})).
		Exec(ctx)
	return err
}

// UpdateTaskRelatedDemandsToCompletedWithoutPayment 全自购直跳完成场景：
// demand 从 distributing 直接到 completed，没有支付环节，不写 payment_completed_at。
func UpdateTaskRelatedDemandsToCompletedWithoutPayment(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
	now time.Time,
) error {
	_, err := db.NewUpdate().
		Model((*model.ErrandDemand)(nil)).
		Set("status = ?", model.ErrandDemandStatusCompleted).
		Set("distribution_completed_at = ?", now).
		Set("updated_at = ?", now).
		Where(`id IN (
			SELECT DISTINCT edi.errand_demand_id
			FROM errand.errand_task_assignment AS eta
			JOIN errand.errand_demand_item AS edi ON edi.id = eta.demand_item_id
			WHERE eta.task_id = ?
		)`, taskID).
		Where("status IN (?)", bun.List([]model.ErrandDemandStatus{
			model.ErrandDemandStatusDistributing,
			model.ErrandDemandStatusCompleted,
		})).
		Exec(ctx)
	return err
}

// UpdateTaskRelatedDemandItemsToCompletedWithoutPayment 全自购直跳完成场景：
// demand item 从 distributing 直接到 completed，不写支付相关时间戳。
func UpdateTaskRelatedDemandItemsToCompletedWithoutPayment(
	ctx context.Context,
	db bun.IDB,
	taskID int64,
	now time.Time,
) error {
	_, err := db.NewUpdate().
		Model((*model.ErrandDemandItem)(nil)).
		Set("status = ?", model.ErrandDemandItemStatusCompleted).
		Set("updated_at = ?", now).
		Where(`id IN (
			SELECT eta.demand_item_id
			FROM errand.errand_task_assignment AS eta
			WHERE eta.task_id = ?
		)`, taskID).
		Where("status IN (?)", bun.List([]model.ErrandDemandItemStatus{
			model.ErrandDemandItemStatusDistributing,
			model.ErrandDemandItemStatusCompleted,
		})).
		Exec(ctx)
	return err
}

type TaskPaymentBillRefRow struct {
	PayerID          int64  `bun:"payer_id"`
	PaymentBillID    *int64 `bun:"payment_bill_id"`
	AssignmentCount  int64  `bun:"assignment_count"`
	MissingBillCount int64  `bun:"missing_bill_count"`
	BillIDCount      int64  `bun:"bill_id_count"`
}

func ListTaskPaymentBillRefs(ctx context.Context, db bun.IDB, taskID int64) ([]TaskPaymentBillRefRow, error) {
	rows := make([]TaskPaymentBillRefRow, 0)
	err := db.NewSelect().
		TableExpr("errand.errand_task_assignment AS eta").
		Join("JOIN errand.errand_task AS et ON et.id = eta.task_id").
		ColumnExpr("eta.purchaser_id AS payer_id").
		ColumnExpr("MAX(eta.payment_bill_id) AS payment_bill_id").
		ColumnExpr("COUNT(*) AS assignment_count").
		ColumnExpr("COUNT(*) FILTER (WHERE eta.payment_bill_id IS NULL) AS missing_bill_count").
		ColumnExpr("COUNT(DISTINCT eta.payment_bill_id) FILTER (WHERE eta.payment_bill_id IS NOT NULL) AS bill_id_count").
		Where("eta.task_id = ?", taskID).
		Where("eta.distributed_quantity > 0").
		Where("eta.purchaser_id != et.captain_id").
		GroupExpr("eta.purchaser_id").
		OrderExpr("eta.purchaser_id ASC").
		Scan(ctx, &rows)
	return rows, err
}

type ErrandTaskListRow struct {
	TaskID    int64                  `bun:"task_id"`
	StoreID   int64                  `bun:"store_id"`
	StoreName string                 `bun:"store_name"`
	Status    model.ErrandTaskStatus `bun:"status"`
	CreatedAt time.Time              `bun:"created_at"`
}

func CountErrandTasks(
	ctx context.Context,
	db bun.IDB,
	captainID int64,
	status *model.ErrandTaskStatus,
) (int32, error) {
	q := db.NewSelect().
		TableExpr("errand.errand_task AS et").
		Where("et.captain_id = ?", captainID)
	if status != nil {
		q = q.Where("et.status = ?", *status)
	}

	total, err := q.Count(ctx)
	if err != nil {
		return 0, err
	}
	return int32(total), nil //nolint:gosec // task counts are bounded by database size.
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

func ListErrandTaskItems(ctx context.Context, db bun.IDB, taskIDs []int64) ([]ErrandTaskListItemRow, error) {
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
) (time.Time, error) {
	task := &model.ErrandTask{ID: taskID}
	res, err := db.NewUpdate().
		Model(task).
		Set("status = ?", model.ErrandTaskStatusCancelled).
		Set("cancelled_at = ?", now).
		Set("updated_at = ?", now).
		WherePK().
		Where("updated_at = ?", expectedUpdatedAt).
		Where("status NOT IN (?)", bun.List([]model.ErrandTaskStatus{
			model.ErrandTaskStatusCompleted,
			model.ErrandTaskStatusCancelled,
		})).
		Returning("updated_at").
		Exec(ctx)
	if err != nil {
		return time.Time{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return time.Time{}, err
	}
	if affected == 0 {
		return time.Time{}, sql.ErrNoRows
	}
	return task.UpdatedAt, nil
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

func ReopenTaskRelatedDemands(ctx context.Context, db bun.IDB, taskID int64, now time.Time) error {
	_, err := db.NewUpdate().
		Model((*model.ErrandDemand)(nil)).
		Set("status = ?", model.ErrandDemandStatusOpen).
		Set("task_id = NULL").
		Set("shopping_start_at = NULL").
		Set("shopping_completed_at = NULL").
		Set("distribution_completed_at = NULL").
		Set("payment_completed_at = NULL").
		Set("cancelled_at = NULL").
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

func ReopenTaskRelatedDemandItems(ctx context.Context, db bun.IDB, taskID int64, now time.Time) error {
	_, err := db.NewUpdate().
		Model((*model.ErrandDemandItem)(nil)).
		Set("status = ?", model.ErrandDemandItemStatusOpen).
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

func DeleteReopenedTaskAssignments(ctx context.Context, db bun.IDB, taskID int64) error {
	_, err := db.NewDelete().
		Model((*model.ErrandTaskAssignment)(nil)).
		Where("task_id = ?", taskID).
		Where(`demand_item_id IN (
			SELECT edi.id
			FROM errand.errand_demand_item AS edi
			WHERE edi.status = ?
		)`, model.ErrandDemandItemStatusOpen).
		Exec(ctx)
	return err
}
