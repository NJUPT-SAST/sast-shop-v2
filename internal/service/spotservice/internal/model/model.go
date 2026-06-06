package model

import (
	"time"

	"github.com/uptrace/bun"
)

type SpotGoods struct {
	bun.BaseModel `bun:"table:spot.spot_goods,alias:sg"`

	ID                int64      `bun:"id,pk,autoincrement"`
	SellerID          int64      `bun:"seller_id,notnull"`
	StoreID           int64      `bun:"store_id,notnull"`
	ProductTemplateID int64      `bun:"product_template_id,notnull"`
	SalePriceCents    int32      `bun:"sale_price_cents,notnull"`
	StockTotal        int32      `bun:"stock_total,notnull"`
	ClosedAt          *time.Time `bun:"closed_at"`
	CreatedAt         time.Time  `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt         time.Time  `bun:"updated_at,notnull,default:current_timestamp"`
}

type SpotStockLedger struct {
	bun.BaseModel `bun:"table:spot.spot_stock_ledger,alias:ssl"`

	ID         int64             `bun:"id,pk,autoincrement"`
	ListingID  int64             `bun:"listing_id,notnull"`
	Delta      int32             `bun:"delta,notnull"`
	Reason     StockLedgerReason `bun:"reason,notnull,default:'publish'"`
	RefType    string            `bun:"ref_type,notnull,default:''"`
	RefID      *int64            `bun:"ref_id"`
	OperatorID *int64            `bun:"operator_id"`
	CreatedAt  time.Time         `bun:"created_at,notnull,default:current_timestamp"`
}

type SpotOrder struct {
	bun.BaseModel `bun:"table:spot.spot_order,alias:so"`

	ID                  int64           `bun:"id,pk,autoincrement"`
	OrderNo             string          `bun:"order_no,notnull,unique"`
	PurchaserID         int64           `bun:"purchaser_id,notnull"`
	ListingID           int64           `bun:"listing_id,notnull"`
	ProductTemplateID   int64           `bun:"product_template_id,notnull"`
	TitleSnapshot       string          `bun:"title_snapshot,notnull"`
	DescriptionSnapshot string          `bun:"description_snapshot,notnull,default:''"`
	ImageURLSnapshot    string          `bun:"image_url_snapshot,notnull,default:''"`
	Quantity            int32           `bun:"quantity,notnull"`
	UnitPriceCents      int32           `bun:"unit_price_cents,notnull"`
	TotalAmountCents    int32           `bun:"total_amount_cents,notnull"`
	PaymentBillID       *int64          `bun:"payment_bill_id"`
	Status              SpotOrderStatus `bun:"status,notnull,default:'pending_payment'"`
	PaidAt              *time.Time      `bun:"paid_at"`
	CompletedAt         *time.Time      `bun:"completed_at"`
	CancelledAt         *time.Time      `bun:"cancelled_at"`
	CreatedAt           time.Time       `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt           time.Time       `bun:"updated_at,notnull,default:current_timestamp"`
}

type SpotOrderStatus string

const (
	SpotOrderStatusPendingPayment SpotOrderStatus = "pending_payment"
	SpotOrderStatusPaid           SpotOrderStatus = "paid"
	SpotOrderStatusCompleted      SpotOrderStatus = "completed"
	SpotOrderStatusCancelled      SpotOrderStatus = "cancelled"
)

type StockLedgerReason string

const (
	StockLedgerReasonPublish      StockLedgerReason = "publish"
	StockLedgerReasonOrderLock    StockLedgerReason = "order_lock"
	StockLedgerReasonOrderCancel  StockLedgerReason = "order_cancel"
	StockLedgerReasonManualAdjust StockLedgerReason = "manual_adjust"
)
