package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	ac "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/admin/v1/adminv1connect"
	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	wp "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/westpocket/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice/internal/model"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type pocketCorrectionPayments struct {
	ac.UnimplementedPaymentCorrectionInternalServiceHandler
	calls            int
	bills            []*av.SourceBill
	failure          error
	duringCorrection func()
}

func (p *pocketCorrectionPayments) GetSourceBills(
	_ context.Context,
	_ *connect.Request[av.GetSourceBillsRequest],
) (*connect.Response[av.GetSourceBillsResponse], error) {
	return connect.NewResponse(&av.GetSourceBillsResponse{Bills: p.bills}), nil
}

func (p *pocketCorrectionPayments) CorrectSourceBills(
	_ context.Context,
	r *connect.Request[av.CorrectSourceBillsRequest],
) (*connect.Response[av.CorrectSourceBillsResponse], error) {
	p.calls++
	if p.duringCorrection != nil {
		p.duringCorrection()
	}
	if p.failure != nil {
		return nil, p.failure
	}
	out := &av.CorrectSourceBillsResponse{}
	for i, b := range r.Msg.ExpectedBills {
		out.Bills = append(
			out.Bills,
			&av.SourceBill{
				Id:             fmt.Sprint(1000 + i),
				PayerId:        b.PayerId,
				PayeeId:        b.PayeeId,
				AmountCents:    b.AmountCents,
				Status:         "unpaid",
				LifecycleRound: r.Msg.TargetRound,
				UpdatedAt:      timestamppb.Now(),
			},
		)
	}
	return connect.NewResponse(out), nil
}

func setupPocketAdmin(t *testing.T) (*Service, context.Context, *pocketCorrectionPayments) {
	t.Helper()
	s := integrationService(t)
	ctx := context.Background()
	_, e := s.DB.ExecContext(
		ctx,
		`INSERT INTO "user".user_account(id,feishu_open_id,display_name,role) VALUES (1,'admin-test','运营','admin');
 INSERT INTO westpocket.pocket(id,owner_id,title,total_cents,status,participant_count,owner_share_cents,receivable_cents,published_at,qr_ciphertext) VALUES (100,10,'测试活动',600,'settled',2,300,300,now(),'private-qr');
 INSERT INTO westpocket.pocket_member(id,pocket_id,user_id,share_cents,payment_bill_id,bill_status) VALUES (200,100,10,300,NULL,'self_share'),(201,100,20,300,600,'completed');
 INSERT INTO westpocket.async_job(id,kind,owner_id,pocket_id,status,dedupe_key,payload) VALUES (300,'publish',10,100,'failed','publish-test','{"private_payload":"hidden"}'),(301,'recognize',10,100,'failed','face-test','{"private_face":"hidden"}');
 INSERT INTO westpocket.notification_outbox(id,pocket_id,recipient_user_id,kind,status,message_uuid) VALUES (400,100,20,'payment','pending','11111111-1111-4111-8111-111111111111')`,
	)
	if e != nil {
		t.Fatal(e)
	}
	payments := &pocketCorrectionPayments{
		bills: []*av.SourceBill{
			{
				Id:             "600",
				PayerId:        "20",
				PayeeId:        "10",
				AmountCents:    300,
				Status:         "completed",
				LifecycleRound: 1,
				UpdatedAt:      timestamppb.Now(),
			},
		},
	}
	path, h := ac.NewPaymentCorrectionInternalServiceHandler(payments)
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
	previous := config.AppConfig
	config.AppConfig = &config.Config{PaymentServiceURL: "http://" + host, PaymentServicePort: int32(number)}
	t.Cleanup(func() { config.AppConfig = previous })
	t.Setenv("ADMIN_INTERNAL_TOKEN", strings.Repeat("t", 40))
	actor := interceptor.SetUserToContext(
		ctx,
		&interceptor.AuthUser{UserID: 1, Role: "admin", Status: "active", AuthVersion: 1},
	)
	return s, actor, payments
}

func pocketAdminPreview(t *testing.T, s *Service, ctx context.Context) *av.PreviewCorrectionResponse {
	t.Helper()
	p, e := s.adminReadPocket(ctx, s.DB, 100, false)
	if e != nil {
		t.Fatal(e)
	}
	preview, e := s.AdminEngine().
		Preview(ctx, &av.PreviewCorrectionRequest{Resource: av.ResourceKind_RESOURCE_KIND_POCKET, Id: "100", Action: "reverse_completion", ExpectedRound: p.LifecycleRound, ExpectedUpdatedAt: timestamppb.New(p.UpdatedAt), Reason: "原收款确认有误", Evidence: "核对收款人流水未到账", Values: map[string]string{"funds_handling": "confirmation_error"}})
	if e != nil {
		t.Fatal(e)
	}
	return preview
}

//nolint:gocyclo // Exercise partial success, recovery and all stale-event guards in one lifecycle.
func TestAdminPocketCorrectionResumesAfterLocalFailureAndFencesOldRound(t *testing.T) {
	s, ctx, payments := setupPocketAdmin(t)
	preview := pocketAdminPreview(t, s, ctx)
	if _, e := s.DB.ExecContext(
		ctx,
		`ALTER TABLE westpocket.admin_action_log ADD CONSTRAINT block_test_action CHECK(action <> 'reverse_completion')`,
	); e != nil {
		t.Fatal(e)
	}
	request := &av.ExecuteCorrectionRequest{
		PreviewId: preview.PreviewId,
		Digest:    preview.Digest,
		RequestId: "pocket-correction-test",
	}
	first, e := s.AdminEngine().Execute(ctx, request)
	if e != nil {
		t.Fatal(e)
	}
	if first.Correction.Status != "retrying" || payments.calls != 1 {
		t.Fatalf("unexpected partial result: %v calls=%d", first, payments.calls)
	}
	if _, e = s.DB.ExecContext(
		ctx,
		`ALTER TABLE westpocket.admin_action_log DROP CONSTRAINT block_test_action`,
	); e != nil {
		t.Fatal(e)
	}
	final, e := s.AdminEngine().Execute(ctx, request)
	if e != nil {
		t.Fatal(e)
	}
	if final.Correction.Status != "completed" || payments.calls != 1 {
		t.Fatalf("replayed payment or incomplete: %v calls=%d", final, payments.calls)
	}
	pocket, e := s.adminReadPocket(ctx, s.DB, 100, false)
	if e != nil {
		t.Fatal(e)
	}
	if pocket.Status != "collecting" || pocket.LifecycleRound != 2 || pocket.AdminOperationID != "" {
		t.Fatalf("pocket=%+v", pocket)
	}
	var bill int64
	if e = s.DB.NewRaw("SELECT payment_bill_id FROM westpocket.pocket_member WHERE id=201").
		Scan(ctx, &bill); e != nil ||
		bill != 1000 {
		t.Fatalf("bill=%d err=%v", bill, e)
	}
	count, e := s.DB.NewSelect().
		TableExpr("westpocket.admin_round_history").
		Where("resource_id=100 AND lifecycle_round=1").
		Count(ctx)
	if e != nil || count != 1 {
		t.Fatalf("history=%d err=%v", count, e)
	}
	handler := s.LifecycleInterceptor()(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&emptypb.Empty{}), nil
	})
	if _, e = handler(
		ctx,
		connect.NewRequest(&wp.RemindMembersRequest{PocketId: 100, LifecycleRound: 1}),
	); connect.CodeOf(
		e,
	) != connect.CodeAborted {
		t.Fatalf("old round accepted: %v", e)
	}
	if _, e = handler(ctx, connect.NewRequest(&wp.RemindMembersRequest{PocketId: 100, LifecycleRound: 2})); e != nil {
		t.Fatal(e)
	}
	if _, e = s.sendNotification(
		ctx,
		&model.Notification{PocketID: 100, RecipientUserID: 20, Kind: "payment", LifecycleRound: 1},
	); !errors.Is(
		e,
		ErrState,
	) {
		t.Fatalf("old notification accepted: %v", e)
	}
}

func TestAdminPocketProjectionHidesPrivateContentAndRejectsChangedPreview(t *testing.T) {
	s, ctx, payments := setupPocketAdmin(t)
	detail, e := s.AdminGet(ctx, &av.GetRecordRequest{Resource: av.ResourceKind_RESOURCE_KIND_POCKET, Id: "100"})
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(detail.String(), "private") || strings.Contains(detail.String(), "cipher") {
		t.Fatalf("private fields leaked: %v", detail)
	}
	jobs, e := s.AdminList(ctx, &av.ListRecordsRequest{Resource: av.ResourceKind_RESOURCE_KIND_JOB})
	if e != nil {
		t.Fatal(e)
	}
	if jobs.TotalCount != 1 || jobs.Records[0].Id != "300" || strings.Contains(jobs.String(), "payload") {
		t.Fatalf("private jobs leaked: %v", jobs)
	}
	if _, e = s.AdminGet(
		ctx,
		&av.GetRecordRequest{Resource: av.ResourceKind_RESOURCE_KIND_JOB, Id: "301"},
	); connect.CodeOf(
		e,
	) != connect.CodeNotFound {
		t.Fatalf("recognition job exposed: %v", e)
	}
	preview := pocketAdminPreview(t, s, ctx)
	if _, e = s.DB.ExecContext(ctx, `UPDATE westpocket.pocket_member SET share_cents=301 WHERE id=201`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AdminEngine().
		Execute(ctx, &av.ExecuteCorrectionRequest{PreviewId: preview.PreviewId, Digest: preview.Digest, RequestId: "changed-pocket-preview"}); connect.CodeOf(
		e,
	) != connect.CodeAborted {
		t.Fatalf("changed member preview accepted: %v", e)
	}
	if payments.calls != 0 {
		t.Fatal("payment was changed before member version check")
	}
}

func TestAdminPocketRejectedPaymentReleasesWithoutCancellingWork(t *testing.T) {
	s, ctx, payments := setupPocketAdmin(t)
	if _, e := s.DB.ExecContext(ctx, "UPDATE westpocket.async_job SET status='queued' WHERE id=300"); e != nil {
		t.Fatal(e)
	}
	p := pocketAdminPreview(t, s, ctx)
	payments.duringCorrection = func() {
		if e := s.runNextJob(ctx); !errors.Is(e, sql.ErrNoRows) {
			t.Errorf("reserved job was claimed: %v", e)
		}
		if e := s.runNextNotification(ctx); !errors.Is(e, sql.ErrNoRows) {
			t.Errorf("reserved notification was claimed: %v", e)
		}
	}
	payments.failure = connect.NewError(connect.CodeAborted, errors.New("bill changed before reservation"))
	out, e := s.AdminEngine().
		Execute(ctx, &av.ExecuteCorrectionRequest{PreviewId: p.PreviewId, Digest: p.Digest, RequestId: "rejected-payment"})
	if e != nil {
		t.Fatal(e)
	}
	if out.Correction.Status != "failed" {
		t.Fatalf("rejection not released: %v", out)
	}
	pocket, e := s.adminReadPocket(ctx, s.DB, 100, false)
	if e != nil {
		t.Fatal(e)
	}
	if pocket.AdminOperationID != "" || pocket.LifecycleRound != 1 {
		t.Fatalf("source remains reserved: %+v", pocket)
	}
	var job, notice string
	if e = s.DB.NewRaw("SELECT status FROM westpocket.async_job WHERE id=300").Scan(ctx, &job); e != nil {
		t.Fatal(e)
	}
	if e = s.DB.NewRaw("SELECT status FROM westpocket.notification_outbox WHERE id=400").Scan(ctx, &notice); e != nil {
		t.Fatal(e)
	}
	if job != "queued" || notice != "pending" {
		t.Fatalf("reservation destroyed work: job=%s notice=%s", job, notice)
	}
}

func TestAdminPocketZeroReceivableCannotReverseSettlement(t *testing.T) {
	s, ctx, payment := setupPocketAdmin(t)
	payment.bills = nil
	if _, e := s.DB.ExecContext(
		ctx,
		"UPDATE westpocket.pocket SET owner_share_cents=600,receivable_cents=0 WHERE id=100",
	); e != nil {
		t.Fatal(e)
	}
	p, e := s.adminReadPocket(ctx, s.DB, 100, false)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.AdminEngine().
		Preview(ctx, &av.PreviewCorrectionRequest{Resource: av.ResourceKind_RESOURCE_KIND_POCKET, Id: "100", Action: "reverse_completion", ExpectedRound: p.LifecycleRound, ExpectedUpdatedAt: timestamppb.New(p.UpdatedAt), Reason: "核验结算", Evidence: "无应收款", Values: map[string]string{"funds_handling": "confirmation_error"}})
	if connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatalf("zero receivable manufactured payment: %v", e)
	}
}

func TestAdminPocketMixedFundsNeedExplicitUnreceivedEvidence(t *testing.T) {
	s, ctx, payment := setupPocketAdmin(t)
	if _, e := s.DB.ExecContext(
		ctx,
		`UPDATE westpocket.pocket SET participant_count=3,owner_share_cents=200,receivable_cents=400,status='collecting' WHERE id=100;
 UPDATE westpocket.pocket_member SET share_cents=200 WHERE pocket_id=100;
 INSERT INTO westpocket.pocket_member(id,pocket_id,user_id,share_cents,payment_bill_id,bill_status) VALUES(202,100,30,200,601,'submitted')`,
	); e != nil {
		t.Fatal(e)
	}
	payment.bills[0].AmountCents = 200
	payment.bills = append(
		payment.bills,
		&av.SourceBill{
			Id:             "601",
			PayerId:        "30",
			PayeeId:        "10",
			AmountCents:    200,
			Status:         "submitted",
			LifecycleRound: 1,
			UpdatedAt:      timestamppb.Now(),
		},
	)
	p, e := s.adminReadPocket(ctx, s.DB, 100, false)
	if e != nil {
		t.Fatal(e)
	}
	r := &av.PreviewCorrectionRequest{
		Resource:          av.ResourceKind_RESOURCE_KIND_POCKET,
		Id:                "100",
		Action:            "cancel",
		ExpectedRound:     1,
		ExpectedUpdatedAt: timestamppb.New(p.UpdatedAt),
		Reason:            "活动取消",
		Evidence:          "已收款全退，申报款逐笔核对未到账",
		Values:            map[string]string{"funds_handling": "refunded"},
	}
	if _, e = s.AdminEngine().Preview(ctx, r); connect.CodeOf(e) != connect.CodeFailedPrecondition {
		t.Fatalf("mixed funding assumed without verification: %v", e)
	}
	r.Values["submitted_unreceived_confirmed"] = "true"
	if _, e = s.AdminEngine().Preview(ctx, r); e != nil {
		t.Fatal(e)
	}
}
