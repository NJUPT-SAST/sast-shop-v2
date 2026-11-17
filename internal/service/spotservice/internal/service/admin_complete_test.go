package service

import (
	"fmt"
	"testing"

	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
)

func TestAdminCompletesBlockedBuyerOrderWithVerifiedPayment(t *testing.T) {
	ctx, db := setupAdminSpot(t)
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO "user".user_account (id,feishu_open_id,role,status,display_name) VALUES (8,'buyer-8','user','banned','Buyer')`,
	); err != nil {
		t.Fatal(err)
	}
	payments := &correctionPaymentFake{bills: initialCorrectionBills("completed")}
	useCorrectionPayments(t, payments)
	if _, err := db.ExecContext(ctx, `UPDATE spot.spot_order SET payment_bill_id=901 WHERE id=400`); err != nil {
		t.Fatal(err)
	}
	record, err := GetAdminRecord(
		ctx,
		&av.GetRecordRequest{Resource: av.ResourceKind_RESOURCE_KIND_SPOT_ORDER, Id: "400"},
	)
	if err != nil {
		t.Fatal(err)
	}
	req := &av.MutateRecordRequest{
		Resource:          av.ResourceKind_RESOURCE_KIND_SPOT_ORDER,
		Id:                "400",
		Action:            "complete",
		ExpectedUpdatedAt: record.Record.UpdatedAt,
		ExpectedRound:     1,
		RequestId:         "admin-complete-1",
		Reason:            "协助封禁用户处理存量履约",
		Evidence:          "双方核实货物已交付且款项已到账",
	}
	for i := 0; i < 2; i++ {
		out, err := MutateAdminRecord(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if out.Record.Status != "completed" || out.Record.LifecycleRound != 1 {
			t.Fatalf("complete=%v", out)
		}
	}
	count, err := db.NewSelect().TableExpr("spot.admin_action_log").Where("action='complete'").Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("audit count=%d error=%v", count, err)
	}
}

func TestAdminCompleteRejectsIneffectivePayment(t *testing.T) {
	for _, tc := range []struct {
		status             string
		refunded, reversed bool
	}{{"unpaid", false, false}, {"submitted", false, false}, {"completed", true, false}, {"completed", false, true}} {
		t.Run(fmt.Sprintf("%s/refunded=%t/reversed=%t", tc.status, tc.refunded, tc.reversed), func(t *testing.T) {
			ctx, db := setupAdminSpot(t)
			if _, err := db.ExecContext(
				ctx,
				`UPDATE spot.spot_order SET status='paid',payment_bill_id=901 WHERE id=400`,
			); err != nil {
				t.Fatal(err)
			}
			payments := &correctionPaymentFake{bills: initialCorrectionBills(tc.status)}
			payments.bills[1][0].RefundRegistered = tc.refunded
			payments.bills[1][0].ConfirmationReversed = tc.reversed
			useCorrectionPayments(t, payments)
			record, err := GetAdminRecord(
				ctx,
				&av.GetRecordRequest{Resource: av.ResourceKind_RESOURCE_KIND_SPOT_ORDER, Id: "400"},
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = MutateAdminRecord(
				ctx,
				&av.MutateRecordRequest{
					Resource:          av.ResourceKind_RESOURCE_KIND_SPOT_ORDER,
					Id:                "400",
					Action:            "complete",
					ExpectedUpdatedAt: record.Record.UpdatedAt,
					ExpectedRound:     1,
					RequestId:         "invalid-complete",
					Reason:            "核实",
					Evidence:          "已核实实物",
				},
			)
			if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("ineffective payment accepted: %v", err)
			}
		})
	}
}
