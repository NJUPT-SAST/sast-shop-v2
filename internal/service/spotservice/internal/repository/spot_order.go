package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"github.com/uptrace/bun"
)

var ErrNotFound = errors.New("not found")

type SpotOrderRecord struct {
	ID                  int64                 `bun:"id"`
	OrderNo             string                `bun:"order_no"`
	PurchaserID         int64                 `bun:"purchaser_id"`
	ListingID           int64                 `bun:"listing_id"`
	ProductTemplateID   int64                 `bun:"product_template_id"`
	TitleSnapshot       string                `bun:"title_snapshot"`
	DescriptionSnapshot string                `bun:"description_snapshot"`
	ImageURLSnapshot    string                `bun:"image_url_snapshot"`
	Quantity            int32                 `bun:"quantity"`
	UnitPriceCents      int32                 `bun:"unit_price_cents"`
	TotalAmountCents    int32                 `bun:"total_amount_cents"`
	PaymentBillID       *int64                `bun:"payment_bill_id"`
	Status              model.SpotOrderStatus `bun:"status"`
	PaidAt              *time.Time            `bun:"paid_at"`
	CompletedAt         *time.Time            `bun:"completed_at"`
	CancelledAt         *time.Time            `bun:"cancelled_at"`
	CreatedAt           time.Time             `bun:"created_at"`
	UpdatedAt           time.Time             `bun:"updated_at"`
	SellerID            int64                 `bun:"seller_id"`
	StoreID             int64                 `bun:"store_id"`
}

func RunInTx(ctx context.Context, fn func(context.Context, bun.Tx) error) error {
	return postgres.DB.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, fn)
}

func LockSpotGoods(ctx context.Context, tx bun.Tx, listingID int64) (*model.SpotGoods, error) {
	var goods model.SpotGoods
	err := tx.NewSelect().
		Model(&goods).
		Where("id = ?", listingID).
		For("UPDATE").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock spot goods: %w", err)
	}
	return &goods, nil
}

func ListSpotOrders(
	ctx context.Context,
	userID int64,
	storeID int64,
	perspective string,
	status *model.SpotOrderStatus,
	limit int,
	offset int,
) ([]SpotOrderRecord, int, error) {
	countQuery := spotOrderRecordBaseQuery().
		Where("sg.store_id = ?", storeID)
	listQuery := spotOrderRecordBaseQuery().
		Where("sg.store_id = ?", storeID)

	switch perspective {
	case "purchaser":
		countQuery = countQuery.Where("so.purchaser_id = ?", userID)
		listQuery = listQuery.Where("so.purchaser_id = ?", userID)
	case "seller":
		countQuery = countQuery.Where("sg.seller_id = ?", userID)
		listQuery = listQuery.Where("sg.seller_id = ?", userID)
	default:
		return nil, 0, fmt.Errorf("invalid perspective")
	}

	if status != nil {
		countQuery = countQuery.Where("so.status = ?", *status)
		listQuery = listQuery.Where("so.status = ?", *status)
	}

	total, err := countQuery.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("count spot orders: %w", err)
	}

	var orders []SpotOrderRecord
	if err := listQuery.
		OrderExpr("so.created_at DESC, so.id DESC").
		Limit(limit).
		Offset(offset).
		Scan(ctx, &orders); err != nil {
		return nil, 0, fmt.Errorf("list spot orders: %w", err)
	}
	return orders, total, nil
}

func GetSpotOrderRecord(ctx context.Context, orderID int64) (*SpotOrderRecord, error) {
	var order SpotOrderRecord
	err := spotOrderRecordBaseQuery().
		Where("so.id = ?", orderID).
		Scan(ctx, &order)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get spot order record: %w", err)
	}
	return &order, nil
}

func spotOrderRecordBaseQuery() *bun.SelectQuery {
	return postgres.DB.NewSelect().
		TableExpr("spot.spot_order AS so").
		ColumnExpr("so.id").
		ColumnExpr("so.order_no").
		ColumnExpr("so.purchaser_id").
		ColumnExpr("so.listing_id").
		ColumnExpr("so.product_template_id").
		ColumnExpr("so.title_snapshot").
		ColumnExpr("so.description_snapshot").
		ColumnExpr("so.image_url_snapshot").
		ColumnExpr("so.quantity").
		ColumnExpr("so.unit_price_cents").
		ColumnExpr("so.total_amount_cents").
		ColumnExpr("so.payment_bill_id").
		ColumnExpr("so.status").
		ColumnExpr("so.paid_at").
		ColumnExpr("so.completed_at").
		ColumnExpr("so.cancelled_at").
		ColumnExpr("so.created_at").
		ColumnExpr("so.updated_at").
		ColumnExpr("sg.seller_id").
		ColumnExpr("sg.store_id").
		Join("JOIN spot.spot_goods AS sg ON sg.id = so.listing_id")
}

func LockSpotOrder(ctx context.Context, tx bun.Tx, orderID int64) (*model.SpotOrder, error) {
	var order model.SpotOrder
	err := tx.NewSelect().
		Model(&order).
		Where("id = ?", orderID).
		For("UPDATE").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock spot order: %w", err)
	}
	return &order, nil
}

func DecreaseSpotGoodsStock(ctx context.Context, tx bun.Tx, listingID int64, quantity int32) error {
	res, err := tx.NewUpdate().
		Model((*model.SpotGoods)(nil)).
		Set("stock_total = stock_total - ?", quantity).
		Set("updated_at = now()").
		Where("id = ?", listingID).
		Where("stock_total >= ?", quantity).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("decrease spot goods stock: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected rows: %w", err)
	}
	if affected != 1 {
		return ErrNotFound
	}
	return nil
}

func IncreaseSpotGoodsStock(ctx context.Context, tx bun.Tx, listingID int64, quantity int32) error {
	res, err := tx.NewUpdate().
		Model((*model.SpotGoods)(nil)).
		Set("stock_total = stock_total + ?", quantity).
		Set("updated_at = now()").
		Where("id = ?", listingID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("increase spot goods stock: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected rows: %w", err)
	}
	if affected != 1 {
		return ErrNotFound
	}
	return nil
}

func InsertSpotOrder(ctx context.Context, tx bun.Tx, order *model.SpotOrder) error {
	if _, err := tx.NewInsert().Model(order).Returning("*").Exec(ctx); err != nil {
		return fmt.Errorf("insert spot order: %w", err)
	}
	return nil
}

func InsertStockLedger(ctx context.Context, tx bun.Tx, ledger *model.SpotStockLedger) error {
	if _, err := tx.NewInsert().Model(ledger).Exec(ctx); err != nil {
		return fmt.Errorf("insert spot stock ledger: %w", err)
	}
	return nil
}

func AttachPaymentBill(ctx context.Context, tx bun.Tx, orderID int64, billID int64) (*model.SpotOrder, error) {
	order := &model.SpotOrder{ID: orderID}
	res, err := tx.NewUpdate().
		Model(order).
		Set("payment_bill_id = ?", billID).
		Set("updated_at = now()").
		WherePK().
		Where("status = ?", model.SpotOrderStatusPendingPayment).
		Returning("*").
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("attach payment bill: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("read affected rows: %w", err)
	}
	if affected != 1 {
		return nil, ErrNotFound
	}
	return order, nil
}

func MarkSpotOrderCancelled(ctx context.Context, tx bun.Tx, orderID int64) (*model.SpotOrder, error) {
	order := &model.SpotOrder{ID: orderID}
	res, err := tx.NewUpdate().
		Model(order).
		Set("status = ?", model.SpotOrderStatusCancelled).
		Set("cancelled_at = now()").
		Set("updated_at = now()").
		WherePK().
		Returning("*").
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("mark spot order cancelled: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("read affected rows: %w", err)
	}
	if affected != 1 {
		return nil, ErrNotFound
	}
	return order, nil
}

func MarkSpotOrderCompleted(ctx context.Context, tx bun.Tx, orderID int64) (*model.SpotOrder, error) {
	order := &model.SpotOrder{ID: orderID}
	res, err := tx.NewUpdate().
		Model(order).
		Set("status = ?", model.SpotOrderStatusCompleted).
		Set("completed_at = now()").
		Set("updated_at = now()").
		WherePK().
		Returning("*").
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("mark spot order completed: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("read affected rows: %w", err)
	}
	if affected != 1 {
		return nil, ErrNotFound
	}
	return order, nil
}
