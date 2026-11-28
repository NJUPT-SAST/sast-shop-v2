package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/catalog/v1/catalogv1connect"
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

func TestPaidSpotOrderFilterSynchronizesAllPendingPages(t *testing.T) {
	state := newSpotPaymentTestState(t, 101)
	state.paid[101] = true
	status := spotv1.SpotOrderStatus_SPOT_ORDER_STATUS_PAID
	response, err := ListSpotOrder(context.Background(), 10, &spotv1.ListSpotOrderRequest{
		StoreId: 1, Perspective: spotv1.SpotGoodsPerspective_SPOT_GOODS_PERSPECTIVE_PURCHASER,
		FilterStatus: &status, Page: 1, PageSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.TotalCount != 1 || len(response.SpotOrders) != 1 || response.SpotOrders[0].Id != 101 {
		t.Fatalf("paid filter after synchronization = %v", response)
	}
	if state.status[101] != "paid" || state.batchCalls != 2 {
		t.Fatalf("status=%s batches=%d", state.status[101], state.batchCalls)
	}
}

func TestSpotOrderPaymentTransitions(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		paid, complete, confirmDuringCancel bool
		wantStatus                          string
		wantCode                            connect.Code
		wantStock                           int
	}{
		{name: "confirmed payment cannot cancel", paid: true, wantStatus: "pending_payment", wantCode: connect.CodeFailedPrecondition},
		{name: "payment confirmed during cancellation", confirmDuringCancel: true, wantStatus: "pending_payment", wantCode: connect.CodeFailedPrecondition},
		{name: "unpaid order can cancel", wantStatus: "cancelled", wantStock: 1},
		{name: "confirmed pending order can complete", paid: true, complete: true, wantStatus: "completed"},
		{name: "unpaid order cannot complete", complete: true, wantStatus: "pending_payment", wantCode: connect.CodeFailedPrecondition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := newSpotPaymentTestState(t, 1)
			state.paid[1] = tc.paid
			state.confirmDuringCancel = tc.confirmDuringCancel
			err := postgres.DB.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
				if tc.complete {
					return completeSpotOrderInTx(
						ctx,
						tx,
						10,
						&spotv1.CompleteSpotOrderRequest{SpotOrderId: 1, UpdatedAt: timestamppb.New(state.version)},
					)
				}
				return cancelSpotOrderInTx(
					ctx,
					tx,
					10,
					&spotv1.CancelSpotOrderRequest{SpotOrderId: 1, UpdatedAt: timestamppb.New(state.version)},
				)
			})
			if tc.wantCode == 0 && err != nil {
				t.Fatal(err)
			}
			if tc.wantCode != 0 && connect.CodeOf(err) != tc.wantCode {
				t.Fatalf("error=%v want=%v", err, tc.wantCode)
			}
			if state.status[1] != tc.wantStatus || state.stock != tc.wantStock {
				t.Fatalf("status=%s stock=%d", state.status[1], state.stock)
			}
		})
	}
}

func TestPaymentSynchronizationPreservesTerminalOrders(t *testing.T) {
	for _, status := range []string{"cancelled", "completed"} {
		t.Run(status, func(t *testing.T) {
			state := newSpotPaymentTestState(t, 1)
			state.status[1], state.paid[1] = status, true
			billID := int64(1)
			bill, err := getBill(context.Background(), &billID)
			if err != nil {
				t.Fatal(err)
			}
			if err := syncSpotOrderPayment(context.Background(), postgres.DB, 1, &billID, 100, bill); err != nil {
				t.Fatal(err)
			}
			if state.status[1] != status {
				t.Fatalf("terminal status overwritten: %s", state.status[1])
			}
		})
	}
}

func TestCancelSpotOrderKeepsGoodsDelisted(t *testing.T) {
	state := newSpotPaymentTestState(t, 1)
	state.stock = -1
	err := postgres.DB.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		return cancelSpotOrderInTx(ctx, tx, 10, &spotv1.CancelSpotOrderRequest{
			SpotOrderId: 1, UpdatedAt: timestamppb.New(state.version),
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.status[1] != "cancelled" || state.stock != -1 {
		t.Fatalf("status=%s stock=%d", state.status[1], state.stock)
	}
}

func TestListSpotOrderAllStores(t *testing.T) {
	state := newSpotPaymentTestState(t, 3)
	state.storeID[1] = 1
	state.storeID[2] = 2
	state.storeID[3] = 2
	state.paid[2] = true

	allResp, err := ListSpotOrder(context.Background(), 10, &spotv1.ListSpotOrderRequest{
		StoreId:     0,
		Perspective: spotv1.SpotGoodsPerspective_SPOT_GOODS_PERSPECTIVE_PURCHASER,
		Page:        1,
		PageSize:    20,
	})
	if err != nil {
		t.Fatalf("ListSpotOrder(store_id=0) error = %v", err)
	}
	if allResp.TotalCount != 3 || len(allResp.SpotOrders) != 3 {
		t.Fatalf("all stores response = total %d len %d, want 3", allResp.TotalCount, len(allResp.SpotOrders))
	}
	if state.status[2] != "paid" {
		t.Fatalf("store 2 pending order was not synchronized: status=%s", state.status[2])
	}
	if slicesContainSubstring(state.queries, "sg.store_id =") {
		t.Fatalf("unexpected store_id filter for store_id=0 queries: %v", state.queries)
	}
	if state.storeCalls[1] != 1 || state.storeCalls[2] != 1 {
		t.Fatalf("store cache calls = %v, want 1 call per distinct store", state.storeCalls)
	}
}

func TestListSpotOrderFiltersSpecificStore(t *testing.T) {
	state := newSpotPaymentTestState(t, 3)
	state.storeID[1] = 1
	state.storeID[2] = 2
	state.storeID[3] = 2

	storeResp, err := ListSpotOrder(context.Background(), 10, &spotv1.ListSpotOrderRequest{
		StoreId:     2,
		Perspective: spotv1.SpotGoodsPerspective_SPOT_GOODS_PERSPECTIVE_PURCHASER,
		Page:        1,
		PageSize:    20,
	})
	if err != nil {
		t.Fatalf("ListSpotOrder(store_id=2) error = %v", err)
	}
	if storeResp.TotalCount != 2 || len(storeResp.SpotOrders) != 2 {
		t.Fatalf("store 2 response = total %d len %d, want 2", storeResp.TotalCount, len(storeResp.SpotOrders))
	}
	for _, order := range storeResp.SpotOrders {
		if order.Store == nil || order.Store.Id != 2 {
			t.Fatalf("unexpected order store in store 2 response: %+v", order.Store)
		}
	}
	if !slicesContainSubstring(state.queries, "sg.store_id = 2") {
		t.Fatalf("expected sg.store_id = 2 filter in queries: %v", state.queries)
	}
}

func slicesContainSubstring(values []string, substr string) bool {
	for _, value := range values {
		if strings.Contains(value, substr) {
			return true
		}
	}
	return false
}

func TestListSpotOrderRejectsNegativeStoreID(t *testing.T) {
	_, err := ListSpotOrder(context.Background(), 10, &spotv1.ListSpotOrderRequest{
		StoreId:     -1,
		Perspective: spotv1.SpotGoodsPerspective_SPOT_GOODS_PERSPECTIVE_PURCHASER,
		Page:        1,
		PageSize:    20,
	})
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want %v; err = %v", code, connect.CodeInvalidArgument, err)
	}
}

func TestListSpotOrderRejectsOverlongKeyword(t *testing.T) {
	_, err := ListSpotOrder(context.Background(), 10, &spotv1.ListSpotOrderRequest{
		StoreId:     0,
		Perspective: spotv1.SpotGoodsPerspective_SPOT_GOODS_PERSPECTIVE_PURCHASER,
		Page:        1,
		PageSize:    20,
		Keyword:     strings.Repeat("茶", 201),
	})
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want %v; err = %v", code, connect.CodeInvalidArgument, err)
	}
}

func TestListSpotOrderAppliesTrimmedKeywordFilter(t *testing.T) {
	state := newSpotPaymentTestState(t, 2)
	_, err := ListSpotOrder(context.Background(), 10, &spotv1.ListSpotOrderRequest{
		StoreId:     0,
		Perspective: spotv1.SpotGoodsPerspective_SPOT_GOODS_PERSPECTIVE_PURCHASER,
		Page:        1,
		PageSize:    20,
		Keyword:     "  SO-Milk  ",
	})
	if err != nil {
		t.Fatalf("ListSpotOrder with keyword error = %v", err)
	}
	if !slicesContainSubstring(state.queries, "strpos(lower(so.order_no), lower('SO-Milk')) > 0") ||
		!slicesContainSubstring(state.queries, "strpos(lower(so.title_snapshot), lower('SO-Milk')) > 0") ||
		!slicesContainSubstring(state.queries, "strpos(lower(cs.name), lower('SO-Milk')) > 0") {
		t.Fatalf("expected trimmed keyword filter in spot order queries: %v", state.queries)
	}

	blankState := newSpotPaymentTestState(t, 1)
	_, err = ListSpotOrder(context.Background(), 10, &spotv1.ListSpotOrderRequest{
		StoreId:     0,
		Perspective: spotv1.SpotGoodsPerspective_SPOT_GOODS_PERSPECTIVE_PURCHASER,
		Page:        1,
		PageSize:    20,
		Keyword:     "   \t  ",
	})
	if err != nil {
		t.Fatalf("ListSpotOrder with blank keyword error = %v", err)
	}
	if slicesContainSubstring(blankState.queries, "strpos(") {
		t.Fatalf("unexpected keyword filter for whitespace-only keyword: %v", blankState.queries)
	}
}

type spotPaymentTestState struct {
	status              map[int64]string
	paid                map[int64]bool
	storeID             map[int64]int64
	storeCalls          map[int64]int
	queries             []string
	version             time.Time
	stock               int
	batchCalls          int
	confirmDuringCancel bool
}

func (s *spotPaymentTestState) storeFor(id int64) int64 {
	if storeID, ok := s.storeID[id]; ok {
		return storeID
	}
	return 1
}

func newSpotPaymentTestState(t *testing.T, count int) *spotPaymentTestState {
	t.Helper()
	s := &spotPaymentTestState{
		status:     map[int64]string{},
		paid:       map[int64]bool{},
		storeID:    map[int64]int64{},
		storeCalls: map[int64]int{},
		version:    time.Date(2026, 9, 20, 1, 0, 0, 123456000, time.UTC),
	}
	for i := 1; i <= count; i++ {
		s.status[int64(i)] = "pending_payment"
	}
	db := bun.NewDB(sql.OpenDB(s), pgdialect.New())
	oldDB, oldPayment, oldCatalog := postgres.DB, client.PaymentInternalServiceClient, client.CatalogInternalServiceClient
	postgres.DB, client.PaymentInternalServiceClient, client.CatalogInternalServiceClient = db, &spotPaymentTestClient{
		state: s,
	}, spotCatalogTestClient{state: s}
	t.Cleanup(func() {
		postgres.DB, client.PaymentInternalServiceClient, client.CatalogInternalServiceClient = oldDB, oldPayment, oldCatalog
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func (s *spotPaymentTestState) Connect(context.Context) (driver.Conn, error) {
	return &spotPaymentTestConn{s}, nil
}
func (s *spotPaymentTestState) Driver() driver.Driver            { return s }
func (s *spotPaymentTestState) Open(string) (driver.Conn, error) { return &spotPaymentTestConn{s}, nil }

type spotPaymentTestConn struct{ state *spotPaymentTestState }

func (*spotPaymentTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*spotPaymentTestConn) Close() error                { return nil }
func (c *spotPaymentTestConn) Begin() (driver.Tx, error) { return c, nil }
func (*spotPaymentTestConn) Commit() error               { return nil }
func (*spotPaymentTestConn) Rollback() error             { return nil }
func (c *spotPaymentTestConn) ExecContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	if strings.Contains(q, "UPDATE") && strings.Contains(q, "spot_goods") {
		c.state.stock++
		return driver.RowsAffected(1), nil
	}
	if strings.Contains(q, "UPDATE") && strings.Contains(q, "spot_order") {
		id := queryNumber(q, `(?:so\.)?id = (\d+)`, 1)
		target := "paid"
		if strings.Contains(q, "SET status = 'cancelled'") {
			target = "cancelled"
		}
		if strings.Contains(q, "SET status = 'completed'") {
			target = "completed"
		}
		if target == "paid" &&
			(!strings.Contains(q, "(status = 'pending_payment')") || !strings.Contains(q, "(payment_bill_id = ")) {
			return nil, fmt.Errorf("payment synchronization must guard both status and bill: %s", q)
		}
		if target == "paid" && c.state.status[id] != "pending_payment" {
			return driver.RowsAffected(0), nil
		}
		c.state.status[id] = target
		return driver.RowsAffected(1), nil
	}
	return nil, fmt.Errorf("unexpected exec: %s", q)
}

func (c *spotPaymentTestConn) QueryContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Rows, error) {
	if strings.HasPrefix(q, "INSERT ") {
		return &spotPaymentRows{columns: []string{"id"}, values: [][]driver.Value{{int64(1)}}}, nil
	}
	if strings.HasPrefix(q, "UPDATE ") {
		if _, err := c.ExecContext(ctx, q, a); err != nil {
			return nil, err
		}
		return c.orderRows([]int64{queryNumber(q, `(?:so\.)?id = (\d+)`, 1)}, false), nil
	}
	if strings.Contains(q, "spot_goods") && !strings.Contains(q, "spot_order") {
		return &spotPaymentRows{
			columns: []string{"id", "seller_id", "stock_total"},
			values:  [][]driver.Value{{int64(1), int64(20), int64(c.state.stock)}},
		}, nil
	}
	if strings.Contains(q, "FOR UPDATE") {
		return c.orderRows([]int64{1}, false), nil
	}
	c.state.queries = append(c.state.queries, q)
	ids := c.matchingOrderIDs(q)
	if strings.Contains(q, "count(*)") {
		return &spotPaymentRows{columns: []string{"count"}, values: [][]driver.Value{{int64(len(ids))}}}, nil
	}
	limit := int(queryNumber(q, `LIMIT (\d+)`, int64(len(ids))))
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return c.orderRows(ids, true), nil
}

func (c *spotPaymentTestConn) matchingOrderIDs(q string) []int64 {
	ids := []int64{}
	after := queryNumber(q, `so.id > (\d+)`, 0)
	storeFilter := queryNumber(q, `sg\.store_id = (\d+)`, 0)
	for id, status := range c.state.status {
		if id <= after || (storeFilter > 0 && c.state.storeFor(id) != storeFilter) {
			continue
		}
		if strings.Contains(q, "so.status = 'paid'") && status != "paid" {
			continue
		}
		if strings.Contains(q, "so.status = 'pending_payment'") && status != "pending_payment" {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (c *spotPaymentTestConn) orderRows(ids []int64, record bool) *spotPaymentRows {
	columns := []string{
		"id",
		"purchaser_id",
		"listing_id",
		"payment_bill_id",
		"quantity",
		"total_amount_cents",
		"status",
		"updated_at",
	}
	if record {
		columns = append(columns, "seller_id", "store_id")
	}
	rows := &spotPaymentRows{columns: columns}
	for _, id := range ids {
		values := []driver.Value{id, int64(10), int64(1), id, int64(1), int64(100), c.state.status[id], c.state.version}
		if record {
			values = append(values, int64(20), c.state.storeFor(id))
		}
		rows.values = append(rows.values, values)
	}
	return rows
}

func queryNumber(query, pattern string, fallback int64) int64 {
	match := regexp.MustCompile(pattern).FindStringSubmatch(query)
	if len(match) < 2 {
		return fallback
	}
	value, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		panic(err)
	}
	return value
}

type spotPaymentRows struct {
	columns []string
	values  [][]driver.Value
}

func (r *spotPaymentRows) Columns() []string { return r.columns }
func (*spotPaymentRows) Close() error        { return nil }
func (r *spotPaymentRows) Next(values []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(values, r.values[0])
	r.values = r.values[1:]
	return nil
}

type spotPaymentTestClient struct {
	paymentv1connect.PaymentInternalServiceClient
	state *spotPaymentTestState
}

func (c *spotPaymentTestClient) BatchGetBills(
	_ context.Context,
	req *connect.Request[paymentv1.BatchGetBillsRequest],
) (*connect.Response[paymentv1.BatchGetBillsResponse], error) {
	c.state.batchCalls++
	response := &paymentv1.BatchGetBillsResponse{}
	source := "spot_order"
	for _, id := range req.Msg.BillIds {
		status := paymentv1.BillStatus_BILL_STATUS_UNPAID
		if c.state.paid[id] {
			status = paymentv1.BillStatus_BILL_STATUS_COMPLETED
		}
		response.Bills = append(
			response.Bills,
			&paymentv1.Bill{
				Id:          id,
				SourceId:    &id,
				SourceType:  &source,
				AmountCents: 100,
				Status:      status,
				CompletedAt: timestamppb.New(c.state.version.Add(time.Second)),
			},
		)
	}
	return connect.NewResponse(response), nil
}

func (c *spotPaymentTestClient) CancelBillBySource(
	context.Context,
	*connect.Request[paymentv1.CancelBillBySourceRequest],
) (*connect.Response[paymentv1.CancelBillBySourceResponse], error) {
	if c.state.confirmDuringCancel {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("payment completed"))
	}
	return connect.NewResponse(&paymentv1.CancelBillBySourceResponse{}), nil
}

type spotCatalogTestClient struct {
	catalogv1connect.CatalogInternalServiceClient
	state *spotPaymentTestState
}

func (c spotCatalogTestClient) GetStore(
	_ context.Context,
	req *connect.Request[catalogv1.GetStoreRequest],
) (*connect.Response[catalogv1.GetStoreResponse], error) {
	storeID := req.Msg.StoreId
	if storeID <= 0 {
		storeID = 1
	}
	if c.state != nil {
		c.state.storeCalls[storeID]++
	}
	return connect.NewResponse(&catalogv1.GetStoreResponse{Store: &catalogv1.Store{Id: storeID}}), nil
}
