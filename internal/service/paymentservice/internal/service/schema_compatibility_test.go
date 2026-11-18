package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"

	userconnect "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/user/v1/userv1connect"
	wpconnect "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/westpocket/v1/westpocketv1connect"
	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/client"
)

func TestWestPocketBillCreationWithAdminSchema(t *testing.T) {
	db := testutil.NewPostgres(t)
	oldDB := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = oldDB })
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `ALTER TABLE payment.payment_bill
 ADD COLUMN lifecycle_round bigint NOT NULL DEFAULT 1,
 ADD COLUMN admin_operation_id text NOT NULL DEFAULT '',
 ADD COLUMN superseded_at timestamptz,
 ADD COLUMN funding_bill_id bigint;`); err != nil {
		t.Fatal(err)
	}
	var collecting, alipayAvailable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/sast.sastshopv2.westpocket.v1.WestPocketInternalService/GetCollectionState":
			writePocketTestState(t, w, r, collecting.Load(), alipayAvailable.Load())
		case "/sast.sastshopv2.user.v1.UserInternalService/GetUsers":
			if _, err := w.Write(
				[]byte(`{"users":[{"id":"10","name":"payer"},{"id":"20","name":"payee"}]}`),
			); err != nil {
				t.Error(err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	oldWP, oldUser := client.WestPocketInternalServiceClient, client.UserInternalServiceClient
	client.WestPocketInternalServiceClient = wpconnect.NewWestPocketInternalServiceClient(
		server.Client(),
		server.URL,
		connect.WithProtoJSON(),
	)
	client.UserInternalServiceClient = userconnect.NewUserInternalServiceClient(
		server.Client(),
		server.URL,
		connect.WithProtoJSON(),
	)
	t.Cleanup(func() { client.WestPocketInternalServiceClient, client.UserInternalServiceClient = oldWP, oldUser })
	t.Setenv("WEST_POCKET_INTERNAL_TOKEN", "test-only")
	first, err := createWestPocketBill(ctx, 43, 10, 20, 750)
	if err != nil {
		t.Fatal(err)
	}
	if first.Id <= 0 || first.Payer == nil || first.Payee == nil || first.AmountCents != 750 {
		t.Fatalf("invalid bill: %v", first)
	}
	second, err := createWestPocketBill(ctx, 43, 10, 20, 750)
	if err != nil || second.Id != first.Id {
		t.Fatalf("idempotent retry: %v %v", second, err)
	}
	var count int
	if err = db.NewRaw("SELECT count(*) FROM payment.payment_bill WHERE source_type='west_pocket' AND source_id=43").
		Scan(ctx, &count); err != nil ||
		count != 1 {
		t.Fatalf("duplicate bills: %d %v", count, err)
	}

	collecting.Store(true)
	checkWestPocketAlipaySubmission(t, ctx, first, &alipayAvailable)
}

func checkWestPocketAlipaySubmission(
	t *testing.T,
	ctx context.Context,
	first *paymentv1.Bill,
	alipayAvailable *atomic.Bool,
) {
	t.Helper()
	payerCtx := interceptor.SetUserToContext(ctx, &interceptor.AuthUser{UserID: 10})
	channel := paymentv1.Channel_CHANNEL_ALIPAY
	if _, err := PayBill(payerCtx, first.Id, channel, first.UpdatedAt.AsTime()); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("legacy snapshot accepted Alipay: %v", err)
	}
	alipayAvailable.Store(true)
	paid, err := PayBill(payerCtx, first.Id, channel, first.UpdatedAt.AsTime())
	if err != nil {
		t.Fatal(err)
	}
	if paid.Channel != channel || paid.Status != paymentv1.BillStatus_BILL_STATUS_SUBMITTED {
		t.Fatalf("expected submitted Alipay bill, got %v", paid)
	}
}

func writePocketTestState(t *testing.T, w http.ResponseWriter, r *http.Request, collecting, alipay bool) {
	t.Helper()
	var req struct {
		CheckAlipay bool `json:"checkAlipay"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Error(err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	status := "publishing"
	if collecting {
		status = "collecting"
	}
	if err := json.NewEncoder(w).Encode(map[string]any{
		"status":          status,
		"ownerId":         "20",
		"alipayAvailable": collecting && req.CheckAlipay && alipay,
		"members":         []map[string]any{{"userId": "10", "shareCents": 750}},
	}); err != nil {
		t.Error(err)
	}
}

func TestGetPayeeQrCodeIncludesOptionalAlipay(t *testing.T) {
	db := testutil.NewPostgres(t)
	oldDB := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = oldDB })
	ctx := context.Background()
	const alipay = "https://qr.alipay.com/fkx-published"
	if _, err := UpdateQrCode(ctx, 20, paymentv1.Channel_CHANNEL_ALIPAY, alipay); err != nil {
		t.Fatal(err)
	}
	if _, err := GetPayeeQrCode(ctx, 20); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expected missing WeChat prerequisite, got %v", err)
	}
	wechat, err := UpdateQrCode(ctx, 20, paymentv1.Channel_CHANNEL_WECHAT, "wxp://published")
	if err != nil {
		t.Fatal(err)
	}
	qr, err := GetPayeeQrCode(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if qr.Id != wechat.Id || qr.Content != wechat.Content || qr.AlipayQrContent != alipay {
		t.Fatal("expected WeChat metadata and both codes")
	}
}
