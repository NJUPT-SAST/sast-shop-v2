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
	IncompleteCount    int64 `bun:"incomplete_count"`
}

func GetTaskDistributionSummary(ctx context.Context, db bun.IDB, taskID int64) (*TaskDistributionSummaryRow, error) {
	var row TaskDistributionSummaryRow
	err := db.NewSelect().
		TableExpr("errand.errand_task_item AS eti").
		Join(`LEFT JOIN (
			SELECT task_item_id, COALESCE(SUM(distributed_quantity), 0) AS total_distributed
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
	AssignmentID           int64 `bun:"assignment_id"`
	PayerID                int64 `bun:"payer_id"`
	PayeeID                int64 `bun:"payee_id"`
	PackagingFeeCents      int32 `bun:"packaging_fee_cents"`
	ActualUnitPriceCents   int32 `bun:"actual_unit_price_cents"`
	DistributedQuantity    int32 `bun:"distributed_quantity"`
	ServiceFeePerUnitCents int32 `bun:"service_fee_per_unit_cents"`
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
	DemandItemID           int64      `bun:"demand_item_id"`
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
	PayerID                int64      `bun:"payer_id"`
	PayerName              string     `bun:"payer_name"`
	PayerAvatarURL         string     `bun:"payer_avatar_url"`
	PayeeID                int64      `bun:"payee_id"`
	PayeeName              string     `bun:"payee_name"`
	PayeeAvatarURL         string     `bun:"payee_avatar_url"`
}

func ListCollectingPaymentDetails(ctx context.Context, db bun.IDB, taskID int64) ([]CollectingPaymentDetailRow, error) {
	rows := make([]CollectingPaymentDetailRow, 0)
	err := db.NewSelect().
		TableExpr("errand.errand_task_assignment AS eta").
		Join("JOIN errand.errand_task AS et ON et.id = eta.task_id").
		Join("JOIN errand.errand_task_item AS eti ON eti.id = eta.task_item_id").
		Join("JOIN errand.errand_demand_item AS edi ON edi.id = eta.demand_item_id").
		Join("LEFT JOIN payment.payment_bill AS pb ON pb.id = eta.payment_bill_id").
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
		ColumnExpr("pb.id AS payment_bill_id").
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
		ColumnExpr("eta.purchaser_id AS payer_id").
		ColumnExpr("COALESCE(payer.display_name, '') AS payer_name").
		ColumnExpr("COALESCE(payer.avatar_url, '') AS payer_avatar_url").
		ColumnExpr("et.captain_id AS payee_id").
		ColumnExpr("COALESCE(payee.display_name, '') AS payee_name").
		ColumnExpr("COALESCE(payee.avatar_url, '') AS payee_avatar_url").
		Where("eta.task_id = ?", taskID).
		Where("eta.distributed_quantity > 0").
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

type TaskPaymentSummaryRow struct {
	PayerCount          int64 `bun:"payer_count"`
	IncompleteBillCount int64 `bun:"incomplete_bill_count"`
}

func GetTaskPaymentSummary(ctx context.Context, db bun.IDB, taskID int64) (*TaskPaymentSummaryRow, error) {
	var row TaskPaymentSummaryRow
	err := db.NewSelect().
		TableExpr("errand.errand_task_assignment AS eta").
		Join("LEFT JOIN payment.payment_bill AS pb ON pb.id = eta.payment_bill_id").
		ColumnExpr("COUNT(DISTINCT eta.purchaser_id) AS payer_count").
		ColumnExpr(`COUNT(DISTINCT eta.purchaser_id) FILTER (
			WHERE pb.id IS NULL OR pb.status <> ?
		) AS incomplete_bill_count`, "completed").
		Where("eta.task_id = ?", taskID).
		Where("eta.distributed_quantity > 0").
		Scan(ctx, &row)
	if err != nil {
		return nil, err
	}
	return &row, nil
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
