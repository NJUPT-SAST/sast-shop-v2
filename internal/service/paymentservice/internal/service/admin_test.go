package service

import (
	"context"
	"errors"
	"testing"

	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/repository"
	"github.com/uptrace/bun"
)

func paymentAdminDB(t *testing.T) (context.Context, *bun.DB) {
	t.Helper()
	db := testutil.NewPostgres(
		t,
		"002_west_pocket.sql",
		"005_admin_identity.sql",
		"006_admin_catalog_spot.sql",
		"007_admin_corrections.sql",
	)
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = previous })
	_, err := db.ExecContext(
		context.Background(),
		`INSERT INTO "user".user_account(id,feishu_open_id,display_name,role)VALUES(42,'admin','Operator','admin'),(7,'seller','Seller','user'),(8,'buyer','Buyer','user');
 INSERT INTO catalog.catalog_store(id,name,address,created_by_user_id)VALUES(100,'Store','',7);
 INSERT INTO catalog.catalog_product_template(id,store_id,title,price_cents,created_by_user_id)VALUES(200,100,'Milk',300,7);
 INSERT INTO spot.spot_goods(id,seller_id,store_id,product_template_id,sale_price_cents,stock_total)VALUES(300,7,100,200,300,5);
 INSERT INTO spot.spot_order(id,order_no,purchaser_id,listing_id,product_template_id,title_snapshot,quantity,unit_price_cents,total_amount_cents,status)VALUES(400,'SO400',8,300,200,'Milk',1,300,300,'completed');
 INSERT INTO payment.payment_bill(id,bill_no,payer_id,payee_id,source_type,source_id,amount_cents,verify_code,status,completed_at)VALUES(500,'PAY500',8,7,'spot_order',400,300,'1234','completed',now());`,
	)
	if err != nil {
		t.Fatal(err)
	}
	return interceptor.SetUserToContext(
		context.Background(),
		&interceptor.AuthUser{UserID: 42, Role: "admin", Status: "active", AuthVersion: 1},
	), db
}

func sourceRequest(
	t *testing.T,
	ctx context.Context,
	db *bun.DB,
	round int64,
	action, funds, operation string,
) *av.CorrectSourceBillsRequest {
	t.Helper()
	response, err := AdminSourceBills(
		ctx,
		&av.GetSourceBillsRequest{SourceType: "spot_order", SourceId: "400", LifecycleRound: round},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(
		ctx,
		`UPDATE spot.spot_order SET admin_operation_id=? WHERE id=400`,
		operation,
	); err != nil {
		t.Fatal(err)
	}
	return &av.CorrectSourceBillsRequest{
		OperationId:   operation + ":payment",
		ActorId:       "42",
		SourceType:    "spot_order",
		SourceId:      "400",
		ExpectedRound: round,
		TargetRound:   round + 1,
		Action:        action,
		ExpectedBills: response.Bills,
		Reason:        "人工核实纠错",
		Evidence:      "已核对双方记录及款项",
		FundsHandling: funds,
	}
}

func finishSource(t *testing.T, ctx context.Context, db *bun.DB, operation string, round int64) {
	t.Helper()
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT set_config('sast.admin_operation_id',?,true)`, operation); err != nil {
			return err
		}
		_, err := tx.ExecContext(
			ctx,
			`UPDATE spot.spot_order SET lifecycle_round=?,admin_operation_id='' WHERE id=400`,
			round,
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

//nolint:gocyclo // Exercises one retained-payment lifecycle through refund and reopening.
func TestAdminSourceRetainsPaymentWithoutRechargingAndReplays(t *testing.T) {
	ctx, db := paymentAdminDB(t)
	req := sourceRequest(t, ctx, db, 1, "reverse_completion", "retain", "correction-retain-1")
	result, err := CorrectSourceBills(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Bills) != 1 || !result.AllPaid || result.Bills[0].Status != "completed" ||
		result.Bills[0].LifecycleRound != 2 {
		t.Fatalf("payment lost: %v", result)
	}
	again, err := CorrectSourceBills(ctx, req)
	if err != nil || again.Bills[0].Id != result.Bills[0].Id {
		t.Fatalf("replay: %v %v", again, err)
	}
	count, err := db.NewSelect().TableExpr("payment.payment_bill").Count(ctx)
	if err != nil || count != 2 {
		t.Fatalf("duplicate bills: %d %v", count, err)
	}
	finishSource(t, ctx, db, "correction-retain-1", 2)
	req = sourceRequest(t, ctx, db, 2, "cancel", "refunded", "correction-refund-2")
	result, err = CorrectSourceBills(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Bills[0].Status != "closed" || !result.Bills[0].RefundRegistered {
		t.Fatalf("refund not linked: %v", result)
	}
	var origin int64
	if err = db.NewRaw(`SELECT bill_id FROM payment.admin_refund`).Scan(ctx, &origin); err != nil || origin != 500 {
		t.Fatalf("refund origin=%d err=%v", origin, err)
	}
	finishSource(t, ctx, db, "correction-refund-2", 3)
	req = sourceRequest(t, ctx, db, 3, "reopen", "refunded", "correction-reopen-3")
	result, err = CorrectSourceBills(ctx, req)
	if err != nil || result.Bills[0].Status != "unpaid" {
		t.Fatalf("reopen: %v %v", result, err)
	}
	count, err = db.NewSelect().TableExpr("payment.admin_refund").Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("refund repeated: %d %v", count, err)
	}
}

func TestAdminConfirmationErrorDoesNotInventRefund(t *testing.T) {
	ctx, db := paymentAdminDB(t)
	req := sourceRequest(t, ctx, db, 1, "reverse_completion", "confirmation_error", "confirmation-error-1")
	result, err := CorrectSourceBills(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Bills[0].Status != "unpaid" || result.Bills[0].RefundRegistered {
		t.Fatal("incorrect payment facts")
	}
	count, err := db.NewSelect().TableExpr("payment.admin_confirmation_reversal").Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("missing reversal: %d %v", count, err)
	}
	count, err = db.NewSelect().TableExpr("payment.admin_refund").Count(ctx)
	if err != nil || count != 0 {
		t.Fatalf("invented refund: %d %v", count, err)
	}
}

func TestAdminSourceConflictAndAuditFailureAreAtomic(t *testing.T) {
	ctx, db := paymentAdminDB(t)
	req := sourceRequest(t, ctx, db, 1, "reverse_completion", "retain", "correction-conflict")
	req.ExpectedBills[0].AmountCents = 1
	if _, err := CorrectSourceBills(ctx, req); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale plan accepted: %v", err)
	}
	req.ExpectedBills[0].AmountCents = 300
	if _, err := db.ExecContext(
		ctx,
		`ALTER TABLE payment.admin_action_log ADD CONSTRAINT fail_audit CHECK(false)`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := CorrectSourceBills(ctx, req); err == nil {
		t.Fatal("audit failure ignored")
	}
	var old model.PaymentBill
	if err := db.NewSelect().Model(&old).Where("id=500").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if old.SupersededAt != nil {
		t.Fatal("partial supersede committed")
	}
	count, countErr := db.NewSelect().TableExpr("payment.payment_bill").Count(ctx)
	if countErr != nil {
		t.Fatal(countErr)
	}
	if count != 1 {
		t.Fatal("partial bill inserted")
	}
}

func TestAdminBillPreviewExecuteAndStaleRequests(t *testing.T) {
	ctx, db := paymentAdminDB(t)
	if _, err := db.ExecContext(ctx, `UPDATE payment.payment_bill SET status='submitted' WHERE id=500`); err != nil {
		t.Fatal(err)
	}
	detail, err := AdminGetBill(ctx, &av.GetRecordRequest{Resource: av.ResourceKind_RESOURCE_KIND_BILL, Id: "500"})
	if err != nil {
		t.Fatal(err)
	}
	engine := AdminPaymentEngine()
	preview, err := engine.Preview(
		ctx,
		&av.PreviewCorrectionRequest{
			Resource:          av.ResourceKind_RESOURCE_KIND_BILL,
			Id:                "500",
			Action:            "confirm_payment",
			ExpectedUpdatedAt: detail.Record.UpdatedAt,
			ExpectedRound:     1,
			Reason:            "收款核实",
			Evidence:          "收款方核对已到账",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Execute(
		ctx,
		&av.ExecuteCorrectionRequest{
			PreviewId: preview.PreviewId,
			Digest:    preview.Digest,
			RequestId: "confirm-request-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Correction.Status != "completed" {
		t.Fatalf("correction failed: %v", result.Correction)
	}
	again, err := engine.Execute(
		ctx,
		&av.ExecuteCorrectionRequest{
			PreviewId: preview.PreviewId,
			Digest:    preview.Digest,
			RequestId: "confirm-request-1",
		},
	)
	if err != nil || again.Correction.Id != result.Correction.Id {
		t.Fatalf("execute replay: %v", err)
	}
	count, err := db.NewSelect().TableExpr("payment.admin_action_log").Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("audit duplicated: %d %v", count, err)
	}
	bill, err := repository.GetBillByID(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	bill.LifecycleRound = 2
	if err = validateBillRound(bill, []int64{1}); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("old round accepted")
	}
	ordinary := interceptor.SetUserToContext(
		ctx,
		&interceptor.AuthUser{UserID: 8, Role: "user", Status: "active", AuthVersion: 1},
	)
	if _, err = AdminGetBill(
		ordinary,
		&av.GetRecordRequest{Resource: av.ResourceKind_RESOURCE_KIND_BILL, Id: "500"},
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatal("ordinary administrator access")
	}
}

func TestSourceRefundRequiresBusinessCorrectionAndBusyBillsBlockPreview(t *testing.T) {
	ctx, db := paymentAdminDB(t)
	detail, err := AdminGetBill(ctx, &av.GetRecordRequest{Resource: av.ResourceKind_RESOURCE_KIND_BILL, Id: "500"})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range detail.Record.AllowedActions {
		if action == "register_refund" {
			t.Fatal("linked bill exposes independent refund")
		}
	}
	_, err = AdminPaymentEngine().Preview(ctx, &av.PreviewCorrectionRequest{Resource: av.ResourceKind_RESOURCE_KIND_BILL, Id: "500", Action: "register_refund", ExpectedRound: 1, ExpectedUpdatedAt: detail.Record.UpdatedAt, Reason: "核实整单退款", Evidence: "双方收支记录", Values: map[string]string{"funds_handling": "refunded"}})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("linked refund allowed: %v", err)
	}
	if _, err = db.ExecContext(
		ctx,
		`UPDATE payment.payment_bill SET admin_operation_id='another-bill-correction' WHERE id=500`,
	); err != nil {
		t.Fatal(err)
	}
	_, err = AdminSourceBills(
		ctx,
		&av.GetSourceBillsRequest{SourceType: "spot_order", SourceId: "400", LifecycleRound: 1},
	)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("busy bill snapshot allowed: %v", err)
	}
}

func TestPaymentOutboxKeepsFailuresAndIgnoresHistoricalRound(t *testing.T) {
	ctx, db := paymentAdminDB(t)
	if _, err := db.ExecContext(
		ctx,
		`UPDATE payment.payment_bill SET source_type='errand_task',source_id=999,status='submitted' WHERE id=500; UPDATE payment.payment_bill SET status='completed' WHERE id=500; UPDATE payment.payment_bill SET status='completed' WHERE id=500`,
	); err != nil {
		t.Fatal(err)
	}
	count, err := db.NewSelect().TableExpr("payment.business_outbox").Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("outbox events=%d err=%v", count, err)
	}
	processPaymentOutbox(ctx)
	var state string
	if err = db.NewRaw(`SELECT status FROM payment.business_outbox`).
		Scan(ctx, &state); err != nil ||
		state != "pending" {
		t.Fatalf("failed delivery lost: %q %v", state, err)
	}
	if _, err = db.ExecContext(
		ctx,
		`UPDATE payment.payment_bill SET superseded_at=now() WHERE id=500; UPDATE payment.business_outbox SET next_attempt_at=now()`,
	); err != nil {
		t.Fatal(err)
	}
	processPaymentOutbox(ctx)
	if err = db.NewRaw(`SELECT status FROM payment.business_outbox`).
		Scan(ctx, &state); err != nil ||
		state != "superseded" {
		t.Fatalf("historical callback retried: %q %v", state, err)
	}
}

func TestDelayedSourceCancellationCannotCloseNewRound(t *testing.T) {
	ctx, db := paymentAdminDB(t)
	req := sourceRequest(t, ctx, db, 1, "reverse_completion", "confirmation_error", "correction-cancel-race")
	if _, err := CorrectSourceBills(ctx, req); err != nil {
		t.Fatal(err)
	}
	finishSource(t, ctx, db, "correction-cancel-race", 2)
	if affected, err := repository.CancelBillBySource(ctx, "spot_order", 400, nil, 1); err != nil || affected != 0 {
		t.Fatalf("stale cancel affected=%d err=%v", affected, err)
	}
	var status string
	if err := db.NewRaw(`SELECT status FROM payment.payment_bill WHERE lifecycle_round=2 AND source_id=400`).
		Scan(ctx, &status); err != nil ||
		status != "unpaid" {
		t.Fatalf("new bill closed: %q %v", status, err)
	}
}

func TestOrdinaryErrandCancellationRejectsPartialConfirmedPayments(t *testing.T) {
	ctx, db := paymentAdminDB(t)
	if _, err := db.ExecContext(
		ctx,
		`UPDATE payment.payment_bill SET source_type='errand_task',source_id=999 WHERE id=500; INSERT INTO payment.payment_bill(bill_no,payer_id,payee_id,source_type,source_id,amount_cents,verify_code,status)VALUES('PAY501',42,7,'errand_task',999,500,'4321','unpaid')`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CancelBillBySource(
		ctx,
		"errand_task",
		999,
		nil,
		1,
	); !errors.Is(
		err,
		repository.ErrBillAlreadyCompleted,
	) {
		t.Fatalf("partially paid cancellation accepted: %v", err)
	}
	var status string
	if err := db.NewRaw(`SELECT status FROM payment.payment_bill WHERE bill_no='PAY501'`).
		Scan(ctx, &status); err != nil ||
		status != "unpaid" {
		t.Fatalf("partial close: %q %v", status, err)
	}
}

func TestMixedConfirmedAndSubmittedBillsRequireExplicitUnreceivedEvidence(t *testing.T) {
	ctx, db := paymentAdminDB(t)
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO errand.errand_task(id,task_no,captain_id,store_id,status)VALUES(400,'TASK400',7,100,'collecting_payment'); UPDATE payment.payment_bill SET source_type='errand_task' WHERE id=500; INSERT INTO payment.payment_bill(bill_no,payer_id,payee_id,source_type,source_id,amount_cents,verify_code,status)VALUES('PAY501',42,7,'errand_task',400,500,'4321','submitted')`,
	); err != nil {
		t.Fatal(err)
	}
	snapshot, err := AdminSourceBills(
		ctx,
		&av.GetSourceBillsRequest{SourceType: "errand_task", SourceId: "400", LifecycleRound: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(
		ctx,
		`UPDATE errand.errand_task SET admin_operation_id='mixed-refund-case' WHERE id=400`,
	); err != nil {
		t.Fatal(err)
	}
	req := &av.CorrectSourceBillsRequest{
		OperationId:   "mixed-refund-case:payment",
		ActorId:       "42",
		SourceType:    "errand_task",
		SourceId:      "400",
		ExpectedRound: 1,
		TargetRound:   2,
		Action:        "cancel",
		ExpectedBills: snapshot.Bills,
		Reason:        "整单取消并核对收支",
		Evidence:      "已收款300分全退，另一申报500分经双方核实未实际到账",
		FundsHandling: "refunded",
	}
	if _, err = CorrectSourceBills(ctx, req); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("implicit unpaid assumption: %v", err)
	}
	count, err := db.NewSelect().TableExpr("payment.admin_refund").Count(ctx)
	if err != nil || count != 0 {
		t.Fatalf("partial refund persisted: %d %v", count, err)
	}
	req.SubmittedUnreceivedConfirmed = true
	result, err := CorrectSourceBills(ctx, req)
	if err != nil || len(result.Bills) != 2 {
		t.Fatalf("mixed correction: %v %v", result, err)
	}
	for _, b := range result.Bills {
		if b.Status != "closed" || b.LifecycleRound != 2 {
			t.Fatalf("bill not closed atomically: %v", b)
		}
	}
	var total int64
	if err = db.NewRaw(`SELECT sum(amount_cents) FROM payment.admin_refund`).
		Scan(ctx, &total); err != nil ||
		total != 300 {
		t.Fatalf("invented refund: %d %v", total, err)
	}
}
