package model

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
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

func GenerateBillNo() string {
	ts := time.Now().Format("20060102150405")
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "PAY" + ts + fmt.Sprintf("%06d", time.Now().UnixNano()%1_000_000)
	}
	return "PAY" + ts + fmt.Sprintf("%06d", n.Int64())
}

func GenerateVerifyCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(9000))
	if err != nil {
		return fmt.Sprintf("%04d", time.Now().UnixNano()%9000+1000)
	}
	return fmt.Sprintf("%04d", n.Int64()+1000)
}

func IsValidPaymentChannel(ch PaymentChannel) bool {
	switch ch {
	case PaymentChannelWechat, PaymentChannelAlipay:
		return true
	default:
		return false
	}
}

func ProtoStatusToModel(proto paymentv1.BillStatus) (PaymentBillStatus, bool) {
	switch proto {
	case paymentv1.BillStatus_BILL_STATUS_UNPAID:
		return PaymentBillStatusUnpaid, true
	case paymentv1.BillStatus_BILL_STATUS_SUBMITTED:
		return PaymentBillStatusSubmitted, true
	case paymentv1.BillStatus_BILL_STATUS_COMPLETED:
		return PaymentBillStatusCompleted, true
	case paymentv1.BillStatus_BILL_STATUS_CLOSED:
		return PaymentBillStatusClosed, true
	default:
		return "", false
	}
}

func ModelStatusToProto(model PaymentBillStatus) paymentv1.BillStatus {
	switch model {
	case PaymentBillStatusUnpaid:
		return paymentv1.BillStatus_BILL_STATUS_UNPAID
	case PaymentBillStatusSubmitted:
		return paymentv1.BillStatus_BILL_STATUS_SUBMITTED
	case PaymentBillStatusCompleted:
		return paymentv1.BillStatus_BILL_STATUS_COMPLETED
	case PaymentBillStatusClosed:
		return paymentv1.BillStatus_BILL_STATUS_CLOSED
	default:
		return 0
	}
}

func ProtoChannelToModel(proto paymentv1.Channel) (PaymentChannel, bool) {
	switch proto {
	case paymentv1.Channel_CHANNEL_WECHAT:
		return PaymentChannelWechat, true
	case paymentv1.Channel_CHANNEL_ALIPAY:
		return PaymentChannelAlipay, true
	default:
		return "", false
	}
}
