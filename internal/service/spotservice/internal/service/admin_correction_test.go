package service

import (
	"context"
	"errors"
	"strconv"
	"testing"

	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/adminflow"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/repository"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type correctionPaymentFake struct {
	bills      map[int64][]*av.SourceBill
	calls      int
	failNext   bool
	rejectNext bool
}

func useCorrectionPayments(t *testing.T, state *correctionPaymentFake) {
	t.Helper()
	oldGet, oldCorrect := getSpotCorrectionBills, correctSpotBills
	t.Cleanup(func() { getSpotCorrectionBills, correctSpotBills = oldGet, oldCorrect })
	getSpotCorrectionBills = func(ctx context.Context, source string, id, round int64) ([]*av.SourceBill, error) {
		return state.bills[round], nil
	}
	correctSpotBills = func(ctx context.Context, c *adminflow.Case, source string, target int64, bills []*av.SourceBill, funds string, restoration ...[]*av.SourceBill) (*av.CorrectSourceBillsResponse, error) {
		state.calls++
		if state.rejectNext {
			state.rejectNext = false
			return nil, adminflow.Failure(connect.CodeAborted, "账单已更新")
		}
		if state.failNext {
			state.failNext = false
			return nil, errors.New("temporary payment outage")
		}
		original := bills
		if len(original) == 0 && len(restoration) > 0 {
			original = restoration[0]
		}
		var next []*av.SourceBill
		for _, bill := range original {
			item, ok := proto.Clone(bill).(*av.SourceBill)
			if !ok {
				t.Fatal("invalid source bill clone")
			}
			item.Id = strconv.FormatInt(900+target, 10)
			item.LifecycleRound = target
			switch {
			case c.Action == "cancel":
				item.Status = "closed"
			case funds == "retain":
				item.Status = "completed"
			default:
				item.Status = "unpaid"
			}
			item.RefundRegistered = funds == "refunded"
			next = append(next, item)
		}
		state.bills[target] = next
		return &av.CorrectSourceBillsResponse{
			Bills:   next,
			AllPaid: len(next) == 1 && next[0].Status == "completed",
		}, nil
	}
}

func initialCorrectionBills(status string) map[int64][]*av.SourceBill {
	return map[int64][]*av.SourceBill{
		1: {{Id: "901", PayerId: "8", PayeeId: "7", AmountCents: 300, Status: status, LifecycleRound: 1}},
	}
}

func previewSpot(
	t *testing.T,
	ctx context.Context,
	action string,
	values map[string]string,
) *av.PreviewCorrectionResponse {
	t.Helper()
	record, err := GetAdminRecord(
		ctx,
		&av.GetRecordRequest{Resource: av.ResourceKind_RESOURCE_KIND_SPOT_ORDER, Id: "400"},
	)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := SpotCorrectionEngine().Preview(ctx, &av.PreviewCorrectionRequest{Resource: av.ResourceKind_RESOURCE_KIND_SPOT_ORDER, Id: "400", Action: action, ExpectedUpdatedAt: record.Record.UpdatedAt, ExpectedRound: record.Record.LifecycleRound, Reason: "修正经核实的订单记录", Evidence: "双方核对并记录实物和付款依据", Values: values})
	if err != nil {
		t.Fatal(err)
	}
	return preview
}

func executeSpot(
	t *testing.T,
	ctx context.Context,
	preview *av.PreviewCorrectionResponse,
	key string,
) *av.ExecuteCorrectionResponse {
	t.Helper()
	out, err := SpotCorrectionEngine().Execute(ctx, &av.ExecuteCorrectionRequest{PreviewId: preview.PreviewId, Digest: preview.Digest, RequestId: key})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSpotCorrectionCancelAndReopenPreserveIDAndStockExactlyOnce(t *testing.T) {
	for _, handling := range []string{"restore", "keep"} {
		t.Run(handling, func(t *testing.T) {
			ctx, db := setupAdminSpot(t)
			payments := &correctionPaymentFake{bills: initialCorrectionBills("unpaid")}
			useCorrectionPayments(t, payments)
			preview := previewSpot(
				t,
				ctx,
				"cancel",
				map[string]string{"funds_handling": "no_payment_received", "stock_handling": handling},
			)
			out := executeSpot(t, ctx, preview, "cancel-request-1")
			if out.Correction.Status != "completed" {
				t.Fatalf("cancel = %v", out)
			}
			again := executeSpot(t, ctx, preview, "cancel-request-1")
			if again.Correction.Status != "completed" || payments.calls != 1 {
				t.Fatalf("duplicate cancel = %v calls=%d", again, payments.calls)
			}
			expectedStock := int32(5)
			if handling == "restore" {
				expectedStock = 6
			}
			assertCorrectedSpotState(t, ctx, db, model.SpotOrderStatusCancelled, 2, expectedStock)

			preview = previewSpot(t, ctx, "reopen", map[string]string{"funds_handling": "no_payment_received"})
			out = executeSpot(t, ctx, preview, "reopen-request-1")
			if out.Correction.Status != "completed" {
				t.Fatalf("reopen = %v", out)
			}
			assertCorrectedSpotState(t, ctx, db, model.SpotOrderStatusPendingPayment, 3, 5)
			assertOldSpotRequestsRejected(t, ctx, db)

			detail, err := GetAdminRecord(
				ctx,
				&av.GetRecordRequest{Resource: av.ResourceKind_RESOURCE_KIND_SPOT_ORDER, Id: "400"},
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(detail.RelatedRecords) != 2 {
				t.Fatalf("history=%v", detail.RelatedRecords)
			}
			count, err := db.NewSelect().
				TableExpr("spot.spot_stock_ledger").
				Where("ref_type LIKE 'admin_correction:%'").
				Count(ctx)
			if err != nil {
				t.Fatal(err)
			}
			expectedCount := 0
			if handling == "restore" {
				expectedCount = 2
			}
			if count != expectedCount {
				t.Fatalf("stock ledger count=%d want %d", count, expectedCount)
			}
		})
	}
}

func TestSpotCorrectionRechecksBillsBeforeReserving(t *testing.T) {
	ctx, db := setupAdminSpot(t)
	if _, err := db.ExecContext(
		ctx,
		`UPDATE spot.spot_order SET status='cancelled',inventory_state='released' WHERE id=400`,
	); err != nil {
		t.Fatal(err)
	}
	payments := &correctionPaymentFake{bills: initialCorrectionBills("closed")}
	useCorrectionPayments(t, payments)
	preview := previewSpot(t, ctx, "reopen", map[string]string{"funds_handling": "no_payment_received"})
	payments.bills[1][0].Status = "completed"
	_, err := SpotCorrectionEngine().Execute(ctx, &av.ExecuteCorrectionRequest{PreviewId: preview.PreviewId, Digest: preview.Digest, RequestId: "changed-bill"})
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale bill snapshot was accepted: %v", err)
	}
	assertCorrectedSpotState(t, ctx, db, model.SpotOrderStatusCancelled, 1, 5)
	if payments.calls != 0 {
		t.Fatal("payment mutated before snapshot validation")
	}
}

func TestSpotCorrectionDefiniteRejectionReleasesReservedStock(t *testing.T) {
	ctx, db := setupAdminSpot(t)
	if _, err := db.ExecContext(
		ctx,
		`UPDATE spot.spot_order SET status='cancelled',inventory_state='released' WHERE id=400`,
	); err != nil {
		t.Fatal(err)
	}
	payments := &correctionPaymentFake{bills: initialCorrectionBills("closed"), rejectNext: true}
	useCorrectionPayments(t, payments)
	preview := previewSpot(t, ctx, "reopen", map[string]string{"funds_handling": "no_payment_received"})
	out := executeSpot(t, ctx, preview, "definite-rejection")
	if out.Correction.Status != "failed" {
		t.Fatalf("definite rejection not released: %v", out)
	}
	assertCorrectedSpotState(t, ctx, db, model.SpotOrderStatusCancelled, 1, 5)
	var order model.SpotOrder
	if err := db.NewSelect().Model(&order).Where("id=400").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if order.AdminOperationID != "" {
		t.Fatal("definite rejection left the order locked")
	}
	preview = previewSpot(t, ctx, "reopen", map[string]string{"funds_handling": "no_payment_received"})
	out = executeSpot(t, ctx, preview, "after-definite-rejection")
	if out.Correction.Status != "completed" {
		t.Fatalf("new preview after rejection: %v", out)
	}
	assertCorrectedSpotState(t, ctx, db, model.SpotOrderStatusPendingPayment, 2, 4)
}

func TestSpotCorrectionErroneousReceiptConfirmation(t *testing.T) {
	for _, tc := range []struct{ action, status string }{{"cancel", "paid"}, {"reopen", "cancelled"}, {"reverse_completion", "completed"}} {
		t.Run(tc.action, func(t *testing.T) {
			ctx, db := setupAdminSpot(t)
			if _, err := db.ExecContext(
				ctx,
				`UPDATE spot.spot_order SET status=? WHERE id=400`,
				tc.status,
			); err != nil {
				t.Fatal(err)
			}
			payments := &correctionPaymentFake{bills: initialCorrectionBills("completed")}
			useCorrectionPayments(t, payments)
			values := map[string]string{"funds_handling": "confirmation_error"}
			if tc.action == "cancel" {
				values["stock_handling"] = "keep"
			}
			preview := previewSpot(t, ctx, tc.action, values)
			out := executeSpot(t, ctx, preview, "wrong-confirmation")
			if out.Correction.Status != "completed" {
				t.Fatalf("correction=%v", out)
			}
			status := model.SpotOrderStatusPendingPayment
			if tc.action == "cancel" {
				status = model.SpotOrderStatusCancelled
			}
			assertCorrectedSpotState(t, ctx, db, status, 2, 5)
			if payments.bills[1][0].Status != "completed" || payments.bills[2][0].Status == "completed" {
				t.Fatalf("receipt history or new-round obligation incorrect: %+v", payments.bills)
			}
		})
	}
}

func assertOldSpotRequestsRejected(t *testing.T, ctx context.Context, db *bun.DB) {
	t.Helper()
	var order model.SpotOrder
	if err := db.NewSelect().Model(&order).Where("id=400").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	for _, action := range []func(context.Context, bun.Tx) error{
		func(ctx context.Context, tx bun.Tx) error {
			return cancelSpotOrderInTx(ctx, tx, 8, &spotv1.CancelSpotOrderRequest{SpotOrderId: 400, UpdatedAt: timestamppb.New(order.UpdatedAt), LifecycleRound: 1})
		},
		func(ctx context.Context, tx bun.Tx) error {
			return completeSpotOrderInTx(ctx, tx, 8, &spotv1.CompleteSpotOrderRequest{SpotOrderId: 400, UpdatedAt: timestamppb.New(order.UpdatedAt)})
		},
	} {
		if err := db.RunInTx(ctx, nil, action); connect.CodeOf(err) != connect.CodeAborted {
			t.Fatalf("old round request reached current order: %v", err)
		}
	}
}

func TestSpotCorrectionRetriesPaymentWithoutReservingStockTwice(t *testing.T) {
	ctx, db := setupAdminSpot(t)
	if _, err := db.ExecContext(
		ctx,
		`UPDATE spot.spot_order SET status='cancelled',inventory_state='released' WHERE id=400`,
	); err != nil {
		t.Fatal(err)
	}
	payments := &correctionPaymentFake{bills: initialCorrectionBills("closed"), failNext: true}
	useCorrectionPayments(t, payments)
	preview := previewSpot(t, ctx, "reopen", map[string]string{"funds_handling": "no_payment_received"})
	first := executeSpot(t, ctx, preview, "reopen-retry-1")
	if first.Correction.Status != "retrying" {
		t.Fatalf("first=%v", first)
	}
	goods, err := repository.GetSpotGoodsByID(ctx, 300)
	if err != nil || goods.StockTotal != 4 {
		t.Fatalf("reservation=%+v err=%v", goods, err)
	}
	if err := UpdateSpotGoodsStock(
		ctx,
		7,
		300,
		-1,
		timestamppb.New(goods.UpdatedAt),
	); connect.CodeOf(
		err,
	) != connect.CodeFailedPrecondition {
		t.Fatalf("seller discarded reserved inventory: %v", err)
	}
	var order model.SpotOrder
	if err := db.NewSelect().Model(&order).Where("id=400").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if order.AdminOperationID == "" {
		t.Fatal("missing durable resource lock")
	}
	if err := validateCancelableSpotOrder(
		&order,
		8,
		order.UpdatedAt,
	); connect.CodeOf(
		err,
	) != connect.CodeFailedPrecondition {
		t.Fatalf("ordinary write while correcting: %v", err)
	}
	second := executeSpot(t, ctx, preview, "reopen-retry-1")
	if second.Correction.Status != "completed" {
		t.Fatalf("second=%v", second)
	}
	goods, err = repository.GetSpotGoodsByID(ctx, 300)
	if err != nil || goods.StockTotal != 4 {
		t.Fatalf("double reserve=%+v err=%v", goods, err)
	}
}

func TestSpotCorrectionRejectsChangedPreviewAndRequiresRestoration(t *testing.T) {
	ctx, db := setupAdminSpot(t)
	payments := &correctionPaymentFake{bills: initialCorrectionBills("unpaid")}
	useCorrectionPayments(t, payments)
	preview := previewSpot(t, ctx, "cancel", map[string]string{"funds_handling": "no_payment_received"})
	if _, err := db.ExecContext(
		ctx,
		`UPDATE spot.spot_order SET updated_at=updated_at+interval '1 second' WHERE id=400`,
	); err != nil {
		t.Fatal(err)
	}
	_, err := SpotCorrectionEngine().Execute(ctx, &av.ExecuteCorrectionRequest{PreviewId: preview.PreviewId, Digest: preview.Digest, RequestId: "changed-preview-1"})
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale preview=%v", err)
	}
	if payments.calls != 0 {
		t.Fatal("payment called before version checks")
	}
	if _, err := db.ExecContext(
		ctx,
		`UPDATE spot.spot_order SET status='cancelled',title_snapshot='',inventory_state='unverified' WHERE id=400`,
	); err != nil {
		t.Fatal(err)
	}
	payments.bills = map[int64][]*av.SourceBill{}
	incomplete := previewSpot(t, ctx, "reopen", map[string]string{"funds_handling": "no_payment_received"})
	if len(incomplete.RestorationFields) != 4 {
		t.Fatalf("missing restoration fields: %v", incomplete.RestorationFields)
	}
	_, err = SpotCorrectionEngine().Execute(ctx, &av.ExecuteCorrectionRequest{PreviewId: incomplete.PreviewId, Digest: incomplete.Digest, RequestId: "missing-restoration"})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("incomplete restoration executed: %v", err)
	}
	restored := previewSpot(
		t,
		ctx,
		"reopen",
		map[string]string{
			"funds_handling":         "no_payment_received",
			"title":                  "Verified milk",
			"inventory_released":     "false",
			"missing_bill_confirmed": "true",
			"missing_bill_status":    "unpaid",
		},
	)
	if len(restored.RestorationFields) != 0 {
		t.Fatalf("restoration remains incomplete: %v", restored.RestorationFields)
	}
	out := executeSpot(t, ctx, restored, "complete-restoration")
	if out.Correction.Status != "completed" {
		t.Fatalf("restoration=%v", out)
	}
	var order model.SpotOrder
	if err := db.NewSelect().Model(&order).Where("id=400").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if order.TitleSnapshot != "Verified milk" || order.PaymentBillID == nil || order.LifecycleRound != 2 {
		t.Fatalf("restored order=%+v", order)
	}
}

func TestSpotReverseCompletionKeepsInventoryAndRequiresFundsEvidence(t *testing.T) {
	ctx, db := setupAdminSpot(t)
	if _, err := db.ExecContext(
		ctx,
		`UPDATE spot.spot_order SET status='completed',paid_at=now()-interval '1 day',completed_at=now() WHERE id=400`,
	); err != nil {
		t.Fatal(err)
	}
	payments := &correctionPaymentFake{bills: initialCorrectionBills("completed")}
	useCorrectionPayments(t, payments)
	record, err := GetAdminRecord(
		ctx,
		&av.GetRecordRequest{Resource: av.ResourceKind_RESOURCE_KIND_SPOT_ORDER, Id: "400"},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = SpotCorrectionEngine().Preview(ctx, &av.PreviewCorrectionRequest{Resource: av.ResourceKind_RESOURCE_KIND_SPOT_ORDER, Id: "400", Action: "reverse_completion", ExpectedUpdatedAt: record.Record.UpdatedAt, ExpectedRound: 1, Reason: "核实", Evidence: "原转账凭据", Values: map[string]string{"funds_handling": "no_payment_received"}})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("paid record erased: %v", err)
	}
	preview := previewSpot(t, ctx, "reverse_completion", map[string]string{"funds_handling": "retain"})
	out := executeSpot(t, ctx, preview, "reverse-completion")
	if out.Correction.Status != "completed" {
		t.Fatalf("reverse=%v", out)
	}
	var order model.SpotOrder
	if err := db.NewSelect().Model(&order).Where("id=400").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	goods, err := repository.GetSpotGoodsByID(ctx, 300)
	if err != nil {
		t.Fatal(err)
	}
	if goods.StockTotal != 5 || order.Status != model.SpotOrderStatusPaid || order.CompletedAt != nil ||
		order.LifecycleRound != 2 ||
		order.PaidAt == nil ||
		order.PaidAt.After(timestamppb.Now().AsTime()) {
		t.Fatalf("reverse changed facts: %+v %+v", order, goods)
	}
}

func assertCorrectedSpotState(
	t *testing.T,
	ctx context.Context,
	db *bun.DB,
	status model.SpotOrderStatus,
	round int64,
	stock int32,
) {
	t.Helper()
	var order model.SpotOrder
	if err := db.NewSelect().Model(&order).Where("id=400").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	goods, err := repository.GetSpotGoodsByID(ctx, 300)
	if err != nil {
		t.Fatal(err)
	}
	if goods.StockTotal != stock || order.OrderNo != "SO400" || order.LifecycleRound != round ||
		order.Status != status {
		t.Fatalf("incorrect correction state: order=%+v goods=%+v", order, goods)
	}
	if status != model.SpotOrderStatusCancelled && order.InventoryState != "reserved" {
		t.Fatalf("restored order did not reserve inventory: %+v", order)
	}
}
