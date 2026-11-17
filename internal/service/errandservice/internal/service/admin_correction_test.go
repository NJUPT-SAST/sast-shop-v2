package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	ac "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/admin/v1/adminv1connect"
	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type errandCorrectionPayments struct {
	ac.UnimplementedPaymentCorrectionInternalServiceHandler
	bills []*av.SourceBill
	calls int
}

func (p *errandCorrectionPayments) GetSourceBills(
	context.Context,
	*connect.Request[av.GetSourceBillsRequest],
) (*connect.Response[av.GetSourceBillsResponse], error) {
	return connect.NewResponse(&av.GetSourceBillsResponse{Bills: p.bills}), nil
}

func (p *errandCorrectionPayments) CorrectSourceBills(
	_ context.Context,
	r *connect.Request[av.CorrectSourceBillsRequest],
) (*connect.Response[av.CorrectSourceBillsResponse], error) {
	p.calls++
	next := []*av.SourceBill{}
	inputs := append(append([]*av.SourceBill{}, r.Msg.ExpectedBills...), r.Msg.RestorationBills...)
	for i, b := range inputs {
		status := "unpaid"
		if r.Msg.Action == "cancel" {
			status = "closed"
		}
		next = append(
			next,
			&av.SourceBill{
				Id:             fmt.Sprint(1000 + p.calls*100 + i),
				PayerId:        b.PayerId,
				PayeeId:        b.PayeeId,
				AmountCents:    b.AmountCents,
				Status:         status,
				LifecycleRound: r.Msg.TargetRound,
				UpdatedAt:      timestamppb.Now(),
			},
		)
	}
	p.bills = next
	return connect.NewResponse(&av.CorrectSourceBillsResponse{Bills: next}), nil
}

func setupErrandAdmin(t *testing.T) (*bun.DB, context.Context, *errandCorrectionPayments) {
	t.Helper()
	db := testutil.NewPostgres(
		t,
		"002_west_pocket.sql",
		"003_spot_goods_delisting.sql",
		"005_admin_identity.sql",
		"006_admin_catalog_spot.sql",
		"007_admin_corrections.sql",
		"008_admin_errand_pocket.sql",
	)
	oldDB := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = oldDB })
	ctx := context.Background()
	_, e := db.ExecContext(
		ctx,
		`INSERT INTO "user".user_account(id,feishu_open_id,display_name,role) VALUES (1,'admin-test','运营','admin');
 INSERT INTO errand.errand_task(id,task_no,captain_id,store_id,status) VALUES (100,'ORIGINAL-TASK',10,50,'completed');
 INSERT INTO errand.errand_demand(id,requester_id,store_id,status,deadline,task_id) VALUES (200,20,50,'completed',now()+interval '1 day',100);
 INSERT INTO errand.errand_demand_item(id,errand_demand_id,requester_id,store_id,product_template_id,estimated_unit_price_cents,quantity,service_fee_per_unit_cents,status) VALUES (300,200,20,50,60,100,2,10,'completed');
 INSERT INTO errand.errand_task_item(id,task_id,product_template_id,title_snapshot,required_quantity,purchased_quantity,actual_unit_price_cents,deadline) VALUES (400,100,60,'原商品快照',2,2,100,now()+interval '1 day');
 INSERT INTO errand.errand_task_assignment(id,task_id,task_item_id,demand_item_id,purchaser_id,distributed_quantity,service_fee_per_unit_cents,payment_bill_id) VALUES (500,100,400,300,20,2,10,600)`,
	)
	if e != nil {
		t.Fatal(e)
	}
	payment := &errandCorrectionPayments{
		bills: []*av.SourceBill{
			{
				Id:             "600",
				PayerId:        "20",
				PayeeId:        "10",
				AmountCents:    220,
				Status:         "completed",
				LifecycleRound: 1,
				UpdatedAt:      timestamppb.Now(),
			},
		},
	}
	path, h := ac.NewPaymentCorrectionInternalServiceHandler(payment)
	mux := http.NewServeMux()
	mux.Handle(path, h)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	host, port, e := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if e != nil {
		t.Fatal(e)
	}
	number, e := strconv.ParseInt(port, 10, 32)
	if e != nil {
		t.Fatal(e)
	}
	oldConfig := config.AppConfig
	config.AppConfig = &config.Config{PaymentServiceURL: "http://" + host, PaymentServicePort: int32(number)}
	t.Cleanup(func() { config.AppConfig = oldConfig })
	t.Setenv("ADMIN_INTERNAL_TOKEN", strings.Repeat("t", 40))
	return db, interceptor.SetUserToContext(
		ctx,
		&interceptor.AuthUser{UserID: 1, Role: "admin", Status: "active", AuthVersion: 1},
	), payment
}

func errandPreviewRequest(
	t *testing.T,
	ctx context.Context,
	kind av.ResourceKind,
	id int64,
	action, funds string,
) *av.PreviewCorrectionRequest {
	t.Helper()
	r, e := readAdminErrand(ctx, postgres.DB, kind, id, false)
	if e != nil {
		t.Fatal(e)
	}
	return &av.PreviewCorrectionRequest{
		Resource:          kind,
		Id:                fmt.Sprint(id),
		Action:            action,
		ExpectedRound:     r.LifecycleRound,
		ExpectedUpdatedAt: timestamppb.New(r.UpdatedAt),
		Reason:            "核验后整单纠错",
		Evidence:          "工单与双方流水核对",
		Values:            map[string]string{"funds_handling": funds},
	}
}

func executeErrandPreview(t *testing.T, ctx context.Context, r *av.PreviewCorrectionRequest) {
	t.Helper()
	p, e := AdminErrandEngine().Preview(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	out, e := AdminErrandEngine().Execute(ctx, &av.ExecuteCorrectionRequest{PreviewId: p.PreviewId, Digest: p.Digest, RequestId: "case-" + p.PreviewId})
	if e != nil {
		t.Fatal(e)
	}
	if out.Correction.Status != "completed" {
		t.Fatalf("correction did not complete: %v", out)
	}
}

//nolint:gocyclo // Verify the same entire graph through reversal, cancellation and reopening.
func TestAdminErrandWholeGraphKeepsIDsAndHistory(t *testing.T) {
	db, ctx, payments := setupErrandAdmin(t)
	executeErrandPreview(
		t,
		ctx,
		errandPreviewRequest(
			t,
			ctx,
			av.ResourceKind_RESOURCE_KIND_ERRAND_TASK,
			100,
			"reverse_completion",
			"confirmation_error",
		),
	)
	var current struct {
		TaskNo, Status string
		LifecycleRound int64
	}
	if e := db.NewRaw("SELECT task_no,status,lifecycle_round FROM errand.errand_task WHERE id=100").
		Scan(ctx, &current); e != nil {
		t.Fatal(e)
	}
	if current.TaskNo != "ORIGINAL-TASK" || current.Status != "collecting_payment" || current.LifecycleRound != 2 {
		t.Fatalf("wrong restored task: %+v", current)
	}
	var snapshot string
	if e := db.NewRaw("SELECT snapshot FROM errand.admin_round_history WHERE resource='errand_task' AND resource_id=100 AND lifecycle_round=1").
		Scan(ctx, &snapshot); e != nil {
		t.Fatal(e)
	}
	var saved adminErrandSnapshot
	if e := json.Unmarshal([]byte(snapshot), &saved); e != nil {
		t.Fatal(e)
	}
	if len(saved.Items) != 1 || len(saved.Assignments) != 1 || len(saved.DemandItems) != 1 ||
		saved.Assignments[0].PaymentBillID == nil ||
		*saved.Assignments[0].PaymentBillID != 600 {
		t.Fatalf("incomplete history: %s", snapshot)
	}
	executeErrandPreview(
		t,
		ctx,
		errandPreviewRequest(t, ctx, av.ResourceKind_RESOURCE_KIND_ERRAND_TASK, 100, "cancel", "no_payment_received"),
	)
	executeErrandPreview(
		t,
		ctx,
		errandPreviewRequest(t, ctx, av.ResourceKind_RESOURCE_KIND_ERRAND_TASK, 100, "reopen", "no_payment_received"),
	)
	var demand struct {
		Status                 string
		LifecycleRound, TaskID int64
	}
	if e := db.NewRaw("SELECT status,lifecycle_round,task_id FROM errand.errand_demand WHERE id=200").
		Scan(ctx, &demand); e != nil {
		t.Fatal(e)
	}
	if demand.Status != "pending_payment" || demand.LifecycleRound != 4 || demand.TaskID != 100 || payments.calls != 3 {
		t.Fatalf("wrong linked demand: %+v calls=%d", demand, payments.calls)
	}
	var assignmentID, billID int64
	if e := db.NewRaw("SELECT id,payment_bill_id FROM errand.errand_task_assignment WHERE demand_item_id=300").
		Scan(ctx, &assignmentID, &billID); e != nil {
		t.Fatal(e)
	}
	if assignmentID != 500 || billID != 1300 {
		t.Fatalf("assignment identity/bill mismatch %d %d", assignmentID, billID)
	}
}

func TestAdminErrandRejectsPartialGraphAndStaleAssignments(t *testing.T) {
	db, ctx, payments := setupErrandAdmin(t)
	request := errandPreviewRequest(
		t,
		ctx,
		av.ResourceKind_RESOURCE_KIND_ERRAND_TASK,
		100,
		"reverse_completion",
		"confirmation_error",
	)
	preview, e := AdminErrandEngine().Preview(ctx, request)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.ExecContext(
		ctx,
		"UPDATE errand.errand_task_assignment SET distributed_quantity=1 WHERE id=500",
	); e != nil {
		t.Fatal(e)
	}
	if _, e = AdminErrandEngine().Execute(ctx, &av.ExecuteCorrectionRequest{PreviewId: preview.PreviewId, Digest: preview.Digest, RequestId: "stale-assignment"}); connect.CodeOf(
		e,
	) != connect.CodeAborted {
		t.Fatalf("stale assignment accepted: %v", e)
	}
	if payments.calls != 0 {
		t.Fatal("payment touched before graph validation")
	}
	if _, e = db.ExecContext(
		ctx,
		`UPDATE errand.errand_task_assignment SET distributed_quantity=2 WHERE id=500;INSERT INTO errand.errand_demand_item(id,errand_demand_id,requester_id,store_id,product_template_id,estimated_unit_price_cents,quantity,service_fee_per_unit_cents) VALUES (301,200,20,50,61,100,1,10)`,
	); e != nil {
		t.Fatal(e)
	}
	if _, e = AdminErrandEngine().Preview(ctx, request); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatalf("partial demand graph accepted: %v", e)
	}
}

func TestAdminErrandHistoricalAssignmentRestorationDoesNotSteal(t *testing.T) {
	db, ctx, payments := setupErrandAdmin(t)
	payments.bills[0].Status = "closed"
	if _, e := db.ExecContext(
		ctx,
		`DELETE FROM errand.errand_task_assignment WHERE id=500;UPDATE errand.errand_task SET status='cancelled' WHERE id=100;UPDATE errand.errand_demand SET status='cancelled',task_id=NULL WHERE id=200`,
	); e != nil {
		t.Fatal(e)
	}
	r := errandPreviewRequest(t, ctx, av.ResourceKind_RESOURCE_KIND_ERRAND_TASK, 100, "reopen", "no_payment_received")
	if _, e := AdminErrandEngine().Preview(ctx, r); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatalf("missing assignment accepted: %v", e)
	}
	r.Values["restore_assignments"] = `[{"task_item_id":"400","demand_item_id":"300","distributed_quantity":2}]`
	p, e := AdminErrandEngine().Preview(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.ExecContext(
		ctx,
		`INSERT INTO errand.errand_task(id,task_no,captain_id,store_id) VALUES(101,'OTHER-TASK',11,50);INSERT INTO errand.errand_task_item(id,task_id,product_template_id,title_snapshot,required_quantity,deadline) VALUES(401,101,60,'other',2,now()+interval '1 day');INSERT INTO errand.errand_task_assignment(id,task_id,task_item_id,demand_item_id,purchaser_id,service_fee_per_unit_cents) VALUES(501,101,401,300,20,10)`,
	); e != nil {
		t.Fatal(e)
	}
	if _, e = AdminErrandEngine().Execute(ctx, &av.ExecuteCorrectionRequest{PreviewId: p.PreviewId, Digest: p.Digest, RequestId: "cannot-steal"}); connect.CodeOf(
		e,
	) != connect.CodeAborted {
		t.Fatalf("stole assignment: %v", e)
	}
	if _, e = db.ExecContext(ctx, "DELETE FROM errand.errand_task_assignment WHERE id=501"); e != nil {
		t.Fatal(e)
	}
	executeErrandPreview(t, ctx, r)
	if payments.calls != 1 {
		t.Fatalf("unexpected payment calls=%d", payments.calls)
	}
}

func TestAdminStandaloneDemandReopenRequiresNewExpiredDeadline(t *testing.T) {
	db, ctx, payments := setupErrandAdmin(t)
	if _, e := db.ExecContext(
		ctx,
		`INSERT INTO errand.errand_demand(id,requester_id,store_id,status,deadline,created_at) VALUES(201,20,50,'open',now()-interval '1 day',now()-interval '2 days');
 INSERT INTO errand.errand_demand_item(id,errand_demand_id,requester_id,store_id,product_template_id,estimated_unit_price_cents,quantity,service_fee_per_unit_cents) VALUES(301,201,20,50,61,100,1,10)`,
	); e != nil {
		t.Fatal(e)
	}
	executeErrandPreview(
		t,
		ctx,
		errandPreviewRequest(t, ctx, av.ResourceKind_RESOURCE_KIND_ERRAND_DEMAND, 201, "cancel", "no_payment_received"),
	)
	r := errandPreviewRequest(t, ctx, av.ResourceKind_RESOURCE_KIND_ERRAND_DEMAND, 201, "reopen", "no_payment_received")
	if _, e := AdminErrandEngine().Preview(ctx, r); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatalf("expired demand restored: %v", e)
	}
	r.Values["deadline"] = time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	executeErrandPreview(t, ctx, r)
	row, e := readAdminErrand(ctx, db, av.ResourceKind_RESOURCE_KIND_ERRAND_DEMAND, 201, false)
	if e != nil {
		t.Fatal(e)
	}
	if row.Status != "open" || row.LifecycleRound != 3 || row.TaskID != nil || payments.calls != 0 {
		t.Fatalf("wrong standalone restoration: %+v payment calls=%d", row, payments.calls)
	}
}

func TestAdminErrandSelfPurchaseCannotCreateReceivableByReversal(t *testing.T) {
	db, ctx, payment := setupErrandAdmin(t)
	payment.bills = nil
	if _, e := db.ExecContext(ctx, "UPDATE errand.errand_task SET captain_id=20 WHERE id=100"); e != nil {
		t.Fatal(e)
	}
	request := errandPreviewRequest(
		t,
		ctx,
		av.ResourceKind_RESOURCE_KIND_ERRAND_TASK,
		100,
		"reverse_completion",
		"confirmation_error",
	)
	if _, e := AdminErrandEngine().Preview(ctx, request); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatalf("self-purchase manufactured receivable: %v", e)
	}
}
