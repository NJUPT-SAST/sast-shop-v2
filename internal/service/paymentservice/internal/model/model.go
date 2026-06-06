package model

import (
	"time"

	"github.com/uptrace/bun"
)

type PaymentQRCode struct {
	bun.BaseModel `bun:"table:payment.payment_qr_code,alias:pqc"`

	ID        int64          `bun:"id,pk,autoincrement"`
	OwnerID   int64          `bun:"owner_id,notnull"`
	Channel   PaymentChannel `bun:"channel,notnull"`
	Content   string         `bun:"content,notnull"`
	CreatedAt time.Time      `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt time.Time      `bun:"updated_at,notnull,default:current_timestamp"`
}

type PaymentBill struct {
	bun.BaseModel `bun:"table:payment.payment_bill,alias:pb"`

	ID           int64             `bun:"id,pk,autoincrement"`
	BillNo       string            `bun:"bill_no,notnull,unique"`
	PayerID      int64             `bun:"payer_id,notnull"`
	PayeeID      int64             `bun:"payee_id,notnull"`
	SourceType   *string           `bun:"source_type"`
	SourceID     *int64            `bun:"source_id"`
	AmountCents  int32             `bun:"amount_cents,notnull"`
	VerifyCode   string            `bun:"verify_code,notnull"`
	Status       PaymentBillStatus `bun:"status,notnull,default:'unpaid'"`
	Channel      *PaymentChannel   `bun:"channel"`
	SerialNumber string            `bun:"serial_number,notnull,default:''"`
	SubmittedAt  *time.Time        `bun:"submitted_at"`
	CompletedAt  *time.Time        `bun:"completed_at"`
	ClosedAt     *time.Time        `bun:"closed_at"`
	CreatedAt    time.Time         `bun:"created_at,notnull,default:current_timestamp"`
	UpdatedAt    time.Time         `bun:"updated_at,notnull,default:current_timestamp"`
}

type PaymentConfirmationLog struct {
	bun.BaseModel `bun:"table:payment.payment_confirmation_log,alias:pcl"`

	ID         int64             `bun:"id,pk,autoincrement"`
	BillID     int64             `bun:"bill_id,notnull"`
	OperatorID int64             `bun:"operator_id,notnull"`
	FromStatus PaymentBillStatus `bun:"from_status,notnull"`
	ToStatus   PaymentBillStatus `bun:"to_status,notnull"`
	CreatedAt  time.Time         `bun:"created_at,notnull,default:current_timestamp"`
}

type PaymentChannel string

const (
	PaymentChannelWechat PaymentChannel = "wechat"
	PaymentChannelAlipay PaymentChannel = "alipay"
)

type PaymentBillStatus string

const (
	PaymentBillStatusUnpaid    PaymentBillStatus = "unpaid"
	PaymentBillStatusSubmitted PaymentBillStatus = "submitted"
	PaymentBillStatusCompleted PaymentBillStatus = "completed"
	PaymentBillStatusClosed    PaymentBillStatus = "closed"
)
