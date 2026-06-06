package model

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

type ErrandDemand struct {
	bun.BaseModel `bun:"table:errand.errand_demand,alias:ed"`

	ID                      int64              `bun:"id,pk,autoincrement"`
	RequesterID             int64              `bun:"requester_id,notnull"`
	StoreID                 int64              `bun:"store_id,notnull"`
	Status                  ErrandDemandStatus `bun:"status,notnull,default:'open'"`
	Deadline                time.Time          `bun:"deadline,notnull"`
	TaskID                  *int64             `bun:"task_id"`
	SplitFromDemandID       *int64             `bun:"split_from_demand_id"`
	ShoppingStartAt         *time.Time         `bun:"shopping_start_at"`
	ShoppingCompletedAt     *time.Time         `bun:"shopping_completed_at"`
	DistributionCompletedAt *time.Time         `bun:"distribution_completed_at"`
	PaymentCompletedAt      *time.Time         `bun:"payment_completed_at"`
	CancelledAt             *time.Time         `bun:"cancelled_at"`
	CreatedAt               time.Time          `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt               time.Time          `bun:"updated_at,notnull,default:current_timestamp"`
}

type ErrandDemandItem struct {
	bun.BaseModel `bun:"table:errand.errand_demand_item,alias:edi"`

	ID                      int64                  `bun:"id,pk,autoincrement"`
	ErrandDemandID          int64                  `bun:"errand_demand_id,notnull"`
	RequesterID             int64                  `bun:"requester_id,notnull"`
	StoreID                 int64                  `bun:"store_id,notnull"`
	ProductTemplateID       int64                  `bun:"product_template_id,notnull"`
	EstimatedUnitPriceCents int32                  `bun:"estimated_unit_price_cents,notnull"`
	Quantity                int32                  `bun:"quantity,notnull"`
	ServiceFeePerUnitCents  int32                  `bun:"service_fee_per_unit_cents,notnull"`
	Status                  ErrandDemandItemStatus `bun:"status,notnull,default:'open'"`
	CreatedAt               time.Time              `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt               time.Time              `bun:"updated_at,notnull,default:current_timestamp"`
}

type ErrandTask struct {
	bun.BaseModel `bun:"table:errand.errand_task,alias:et"`

	ID                      int64            `bun:"id,pk,autoincrement"`
	TaskNo                  string           `bun:"task_no,notnull,unique"`
	CaptainID               int64            `bun:"captain_id,notnull"`
	StoreID                 int64            `bun:"store_id,notnull"`
	Status                  ErrandTaskStatus `bun:"status,notnull,default:'shopping'"`
	PackagingFeeCents       int32            `bun:"packaging_fee_cents,notnull,default:0"`
	ShoppingCompletedAt     *time.Time       `bun:"shopping_completed_at"`
	DistributionCompletedAt *time.Time       `bun:"distribution_completed_at"`
	PaymentCompletedAt      *time.Time       `bun:"payment_completed_at"`
	CancelledAt             *time.Time       `bun:"cancelled_at"`
	CreatedAt               time.Time        `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt               time.Time        `bun:"updated_at,notnull,default:current_timestamp"`
}

type ErrandTaskItem struct {
	bun.BaseModel `bun:"table:errand.errand_task_item,alias:eti"`

	ID                   int64      `bun:"id,pk,autoincrement"`
	TaskID               int64      `bun:"task_id,notnull"`
	ProductTemplateID    int64      `bun:"product_template_id,notnull"`
	TitleSnapshot        string     `bun:"title_snapshot,notnull"`
	DescriptionSnapshot  string     `bun:"description_snapshot,notnull,default:''"`
	ImageURLSnapshot     string     `bun:"image_url_snapshot,notnull,default:''"`
	RequiredQuantity     int32      `bun:"required_quantity,notnull"`
	PurchasedQuantity    *int32     `bun:"purchased_quantity"`
	NonPurchaseReason    string     `bun:"non_purchase_reason,notnull,default:''"`
	ActualUnitPriceCents *int32     `bun:"actual_unit_price_cents"`
	Deadline             time.Time  `bun:"deadline,notnull"`
	HandledAt            *time.Time `bun:"handled_at"`
	CreatedAt            time.Time  `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt            time.Time  `bun:"updated_at,notnull,default:current_timestamp"`
}

type ErrandTaskAssignment struct {
	bun.BaseModel `bun:"table:errand.errand_task_assignment,alias:eta"`

	ID                     int64     `bun:"id,pk,autoincrement"`
	TaskID                 int64     `bun:"task_id,notnull"`
	TaskItemID             int64     `bun:"task_item_id,notnull"`
	DemandItemID           int64     `bun:"demand_item_id,notnull,unique"`
	PurchaserID            int64     `bun:"purchaser_id,notnull"`
	DistributedQuantity    int32     `bun:"distributed_quantity,notnull,default:0"`
	ServiceFeePerUnitCents int32     `bun:"service_fee_per_unit_cents,notnull"`
	PaymentBillID          *int64    `bun:"payment_bill_id"`
	CreatedAt              time.Time `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt              time.Time `bun:"updated_at,notnull,default:current_timestamp"`
}

type ErrandPriceChangeLog struct {
	bun.BaseModel `bun:"table:errand.errand_price_change_log,alias:epcl"`

	ID                int64     `bun:"id,pk,autoincrement"`
	TaskItemID        int64     `bun:"task_item_id,notnull"`
	OperatorID        int64     `bun:"operator_id,notnull"`
	OldUnitPriceCents *int32    `bun:"old_unit_price_cents"`
	NewUnitPriceCents int32     `bun:"new_unit_price_cents,notnull"`
	CreatedAt         time.Time `bun:"created_at,notnull,default:current_timestamp"`
}

type ErrandActionLog struct {
	bun.BaseModel `bun:"table:errand.errand_action_log,alias:eal"`

	ID          int64           `bun:"id,pk,autoincrement"`
	TaskID      int64           `bun:"task_id,notnull"`
	ActorID     int64           `bun:"actor_id,notnull"`
	TargetType  string          `bun:"target_type,notnull"`
	TargetID    int64           `bun:"target_id,notnull"`
	Action      string          `bun:"action,notnull"`
	BeforeState json.RawMessage `bun:"before_state,notnull,default:'{}'::jsonb"`
	AfterState  json.RawMessage `bun:"after_state,notnull,default:'{}'::jsonb"`
	CreatedAt   time.Time       `bun:"created_at,notnull,default:current_timestamp"`
}

type ErrandDemandStatus string

const (
	ErrandDemandStatusOpen                ErrandDemandStatus = "open"
	ErrandDemandStatusShopping            ErrandDemandStatus = "shopping"
	ErrandDemandStatusPendingDistributing ErrandDemandStatus = "pending_distributing"
	ErrandDemandStatusDistributing        ErrandDemandStatus = "distributing"
	ErrandDemandStatusPendingPayment      ErrandDemandStatus = "pending_payment"
	ErrandDemandStatusCompleted           ErrandDemandStatus = "completed"
	ErrandDemandStatusCancelled           ErrandDemandStatus = "cancelled"
)

type ErrandDemandItemStatus string

const (
	ErrandDemandItemStatusOpen                ErrandDemandItemStatus = "open"
	ErrandDemandItemStatusShopping            ErrandDemandItemStatus = "shopping"
	ErrandDemandItemStatusPendingDistributing ErrandDemandItemStatus = "pending_distributing"
	ErrandDemandItemStatusDistributing        ErrandDemandItemStatus = "distributing"
	ErrandDemandItemStatusPendingPayment      ErrandDemandItemStatus = "pending_payment"
	ErrandDemandItemStatusCompleted           ErrandDemandItemStatus = "completed"
	ErrandDemandItemStatusCancelled           ErrandDemandItemStatus = "cancelled"
)

type ErrandTaskStatus string

const (
	ErrandTaskStatusShopping            ErrandTaskStatus = "shopping"
	ErrandTaskStatusPendingDistributing ErrandTaskStatus = "pending_distributing"
	ErrandTaskStatusDistributing        ErrandTaskStatus = "distributing"
	ErrandTaskStatusCollectingPayment   ErrandTaskStatus = "collecting_payment"
	ErrandTaskStatusCompleted           ErrandTaskStatus = "completed"
	ErrandTaskStatusCancelled           ErrandTaskStatus = "cancelled"
)
