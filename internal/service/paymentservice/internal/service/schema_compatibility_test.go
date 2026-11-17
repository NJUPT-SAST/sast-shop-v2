package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	userconnect "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/user/v1/userv1connect"
	wpconnect "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/westpocket/v1/westpocketv1connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/sast.sastshopv2.westpocket.v1.WestPocketInternalService/GetCollectionState":
			if _, err := w.Write(
				[]byte(`{"status":"publishing","ownerId":"20","members":[{"userId":"10","shareCents":750}]}`),
			); err != nil {
				t.Error(err)
			}
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
}
