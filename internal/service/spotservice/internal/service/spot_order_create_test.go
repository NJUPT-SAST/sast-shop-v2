package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/payment/v1/paymentv1connect"
	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/client"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCreateSpotOrdersSkipsPaymentOnlyForOwnGoods(t *testing.T) {
	for _, tc := range []struct {
		name       string
		sellerID   int64
		wantStatus spotv1.SpotOrderStatus
		wantBills  int
	}{
		{"own goods", 10, spotv1.SpotOrderStatus_SPOT_ORDER_STATUS_COMPLETED, 0},
		{"other seller", 20, spotv1.SpotOrderStatus_SPOT_ORDER_STATUS_PENDING_PAYMENT, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := newSpotCreateTestState(t, tc.sellerID)
			details, err := CreateSpotOrders(context.Background(), 10, &spotv1.CreateSpotOrdersRequest{
				SpotOrders: []*spotv1.CreateSpotOrder{{
					SpotListingId: 1, Quantity: 2, UpdatedAt: timestamppb.New(state.version),
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(details) != 1 || details[0].Status != tc.wantStatus {
				t.Fatalf("orders = %v, want status %v", details, tc.wantStatus)
			}
			if state.stock != 1 || state.ledgers != 1 || state.billCalls != tc.wantBills {
				t.Fatalf("stock=%d ledgers=%d billCalls=%d", state.stock, state.ledgers, state.billCalls)
			}
			order := details[0]
			if order.Quantity != 2 || order.TotalAmountCents != 200 || order.PaidAt != nil {
				t.Fatalf("unexpected quantity, amount or paid timestamp: %v", order)
			}
			assertSpotCreatePayment(t, order, state, tc.wantBills)
		})
	}
}

func assertSpotCreatePayment(t *testing.T, order *spotv1.SpotOrderDetail, state *spotCreateTestState, wantBills int) {
	t.Helper()
	if wantBills == 0 {
		if order.BillId != 0 || order.Bill != nil || order.CompletedAt == nil || !state.completedInsert {
			t.Fatalf("self purchase must persist completed without a bill: %v", order)
		}
	} else if order.BillId != 900 || order.Bill == nil || order.CompletedAt != nil {
		t.Fatalf("other purchase must remain pending with a bill: %v", order)
	}
}

func TestSelfPurchaseStillValidatesStockAndVersion(t *testing.T) {
	for _, tc := range []struct {
		name       string
		stock      int64
		quantity   int32
		versionAge time.Duration
		wantCode   connect.Code
	}{
		{"insufficient stock", 3, 4, 0, connect.CodeResourceExhausted},
		{"sold out", 0, 1, 0, connect.CodeResourceExhausted},
		{"delisted", -1, 1, 0, connect.CodeFailedPrecondition},
		{"stale version", 3, 1, time.Second, connect.CodeAborted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := newSpotCreateTestState(t, 10)
			state.stock = tc.stock
			_, err := CreateSpotOrders(context.Background(), 10, &spotv1.CreateSpotOrdersRequest{
				SpotOrders: []*spotv1.CreateSpotOrder{{
					SpotListingId: 1, Quantity: tc.quantity,
					UpdatedAt: timestamppb.New(state.version.Add(-tc.versionAge)),
				}},
			})
			if connect.CodeOf(err) != tc.wantCode {
				t.Fatalf("error = %v, want code %v", err, tc.wantCode)
			}
			if state.stock != tc.stock || state.ledgers != 0 || state.billCalls != 0 {
				t.Fatalf("rejected purchase changed stock or created payment: %+v", state)
			}
		})
	}
}

type spotCreateTestState struct {
	sellerID        int64
	version         time.Time
	stock           int64
	ledgers         int
	billCalls       int
	completedInsert bool
}

func newSpotCreateTestState(t *testing.T, sellerID int64) *spotCreateTestState {
	t.Helper()
	s := &spotCreateTestState{sellerID: sellerID, stock: 3, version: time.Now().UTC().Truncate(time.Second)}
	db := bun.NewDB(sql.OpenDB(s), pgdialect.New())
	oldDB, oldPayment, oldCatalog := postgres.DB, client.PaymentInternalServiceClient, client.CatalogInternalServiceClient
	postgres.DB, client.PaymentInternalServiceClient, client.CatalogInternalServiceClient = db,
		&spotCreatePaymentTestClient{state: s}, spotCreateCatalogTestClient{}
	t.Cleanup(func() {
		postgres.DB, client.PaymentInternalServiceClient, client.CatalogInternalServiceClient = oldDB, oldPayment, oldCatalog
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func (s *spotCreateTestState) Connect(context.Context) (driver.Conn, error) {
	return &spotCreateTestConn{state: s}, nil
}
func (s *spotCreateTestState) Driver() driver.Driver { return s }
func (s *spotCreateTestState) Open(string) (driver.Conn, error) {
	return &spotCreateTestConn{state: s}, nil
}

type spotCreateTestConn struct{ state *spotCreateTestState }

func (*spotCreateTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*spotCreateTestConn) Close() error                { return nil }
func (c *spotCreateTestConn) Begin() (driver.Tx, error) { return c, nil }
func (c *spotCreateTestConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c, nil
}
func (*spotCreateTestConn) Commit() error   { return nil }
func (*spotCreateTestConn) Rollback() error { return nil }

func (c *spotCreateTestConn) ExecContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	if strings.HasPrefix(q, "UPDATE ") && strings.Contains(q, "spot_goods") {
		quantity := queryNumber(q, `stock_total = stock_total - (\d+)`, 0)
		if quantity <= 0 || quantity > c.state.stock || !strings.Contains(q, "stock_total >= ") {
			return nil, fmt.Errorf("invalid stock decrement: %s", q)
		}
		c.state.stock -= quantity
		return driver.RowsAffected(1), nil
	}
	return nil, fmt.Errorf("unexpected exec: %s", q)
}

func (c *spotCreateTestConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	if strings.HasPrefix(q, "SELECT ") && strings.Contains(q, "spot_goods") {
		return &spotPaymentRows{
			columns: []string{
				"id",
				"seller_id",
				"store_id",
				"product_template_id",
				"sale_price_cents",
				"stock_total",
				"updated_at",
			},
			values: [][]driver.Value{
				{int64(1), c.state.sellerID, int64(1), int64(2), int64(100), c.state.stock, c.state.version},
			},
		}, nil
	}
	if strings.HasPrefix(q, "INSERT ") && strings.Contains(q, "spot_stock_ledger") {
		if !strings.Contains(q, "'order_lock'") || !strings.Contains(q, "'spot_order'") || !strings.Contains(q, "-2") {
			return nil, fmt.Errorf("invalid stock ledger: %s", q)
		}
		c.state.ledgers++
		return &spotPaymentRows{columns: []string{"id"}, values: [][]driver.Value{{int64(1)}}}, nil
	}
	if strings.HasPrefix(q, "INSERT ") && strings.Contains(q, "spot_order") {
		c.state.completedInsert = strings.Contains(q, "'completed'")
		status := "pending_payment"
		var completedAt driver.Value
		if c.state.completedInsert {
			status, completedAt = "completed", c.state.version
		}
		return &spotPaymentRows{
			columns: []string{"id", "status", "completed_at", "created_at", "updated_at"},
			values:  [][]driver.Value{{int64(50), status, completedAt, c.state.version, c.state.version}},
		}, nil
	}
	if strings.HasPrefix(q, "UPDATE ") && strings.Contains(q, "payment_bill_id = 900") {
		if c.state.completedInsert {
			return nil, errors.New("self purchase must never attach a payment bill")
		}
		return &spotPaymentRows{
			columns: []string{
				"id",
				"quantity",
				"total_amount_cents",
				"payment_bill_id",
				"status",
				"created_at",
				"updated_at",
			},
			values: [][]driver.Value{
				{int64(50), int64(2), int64(200), int64(900), "pending_payment", c.state.version, c.state.version},
			},
		}, nil
	}
	return nil, fmt.Errorf("unexpected query: %s", q)
}

type spotCreatePaymentTestClient struct {
	paymentv1connect.PaymentInternalServiceClient
	state *spotCreateTestState
}

func (c *spotCreatePaymentTestClient) CreateBillForOrder(
	_ context.Context,
	req *connect.Request[paymentv1.CreateBillForOrderRequest],
) (*connect.Response[paymentv1.CreateBillForOrderResponse], error) {
	c.state.billCalls++
	if req.Msg.PayerId == req.Msg.PayeeId {
		return nil, errors.New("self purchase must never create a payment bill")
	}
	return connect.NewResponse(&paymentv1.CreateBillForOrderResponse{Bill: &paymentv1.Bill{Id: 900}}), nil
}

type spotCreateCatalogTestClient struct{ spotCatalogTestClient }

func (spotCreateCatalogTestClient) GetProductTemplate(
	context.Context,
	*connect.Request[catalogv1.GetProductTemplateRequest],
) (*connect.Response[catalogv1.GetProductTemplateResponse], error) {
	return connect.NewResponse(&catalogv1.GetProductTemplateResponse{
		ProductTemplate: &catalogv1.ProductTemplate{Id: 2, Title: "测试商品"},
	}), nil
}
