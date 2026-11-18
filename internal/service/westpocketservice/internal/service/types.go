package service

import (
	"time"

	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
)

type User struct {
	ID              int64
	Name, AvatarURL string
}
type Bill struct {
	LifecycleRound                         int64
	ID, PayerID, PayeeID, SourceID         int64
	AmountCents                            int32
	SourceType, Status, BillNo, VerifyCode string
	UpdatedAt                              time.Time
	Proto                                  *paymentv1.Bill
}
type QR struct {
	ID            int64     `json:"id"`
	Content       string    `json:"content"`
	UpdatedAt     time.Time `json:"updated_at"`
	AlipayContent string    `json:"alipay_content,omitempty"`
}
type Face struct {
	X, Y, Width, Height int
	Status              string
	Candidates          []ProviderCandidate
}
type ProviderCandidate struct {
	PersonID string
	Score    float64
}
