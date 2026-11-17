package adminflow

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func flowFixture(t *testing.T) (context.Context, *Engine, *av.PreviewCorrectionResponse) {
	t.Helper()
	db := testutil.NewPostgres(
		t,
		"002_west_pocket.sql",
		"005_admin_identity.sql",
		"006_admin_catalog_spot.sql",
		"007_admin_corrections.sql",
	)
	if _, err := db.ExecContext(
		context.Background(),
		`INSERT INTO "user".user_account(id,feishu_open_id,display_name,role)VALUES(42,'admin','Admin','admin'); CREATE TABLE public.flow_probe(n integer NOT NULL); INSERT INTO public.flow_probe VALUES(0)`,
	); err != nil {
		t.Fatal(err)
	}
	ctx := interceptor.SetUserToContext(
		context.Background(),
		&interceptor.AuthUser{UserID: 42, Role: "admin", Status: "active", AuthVersion: 1},
	)
	e := &Engine{DB: db, Domain: "payment"}
	e.Build = func(_ context.Context, r *av.PreviewCorrectionRequest) (*Plan, error) {
		return &Plan{
			Record:   &av.AdminRecord{Id: r.Id, Resource: r.Resource, LifecycleRound: r.ExpectedRound},
			Snapshot: json.RawMessage(`{}`),
			Steps:    []Step{{Key: "change", Status: "pending"}},
		}, nil
	}
	e.Reserve = func(context.Context, bun.Tx, *Case) error { return nil }
	e.Apply = func(ctx context.Context, x *Execution) error {
		return x.LocalStep(ctx, "change", func(ctx context.Context, tx bun.Tx) (any, error) {
			_, err := tx.ExecContext(ctx, `UPDATE public.flow_probe SET n=n+1`)
			return true, err
		})
	}
	p, err := e.Preview(
		ctx,
		&av.PreviewCorrectionRequest{
			Id:                "99",
			Resource:          av.ResourceKind_RESOURCE_KIND_BILL,
			ExpectedRound:     1,
			ExpectedUpdatedAt: timestamppb.Now(),
			Action:            "confirm_payment",
			Reason:            "核实记录",
			Evidence:          "双方确认已收到款项",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, e, p
}

func executeRequest(p *av.PreviewCorrectionResponse) *av.ExecuteCorrectionRequest {
	return &av.ExecuteCorrectionRequest{PreviewId: p.PreviewId, Digest: p.Digest, RequestId: "stable-request-123"}
}

func TestExecutionIsIdempotentAndFenced(t *testing.T) {
	ctx, e, p := flowFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := e.Execute(ctx, executeRequest(p)); errs <- err })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := e.Execute(ctx, executeRequest(p))
	if err != nil || result.Correction.Status != "completed" {
		t.Fatalf("completion: %v %v", result, err)
	}
	var n int
	if err = e.DB.NewRaw(`SELECT n FROM public.flow_probe`).Scan(ctx, &n); err != nil || n != 1 {
		t.Fatalf("executions=%d err=%v", n, err)
	}
	req := executeRequest(p)
	req.RequestId = "different-request-123"
	if _, err = e.Execute(ctx, req); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("mismatch accepted: %v", err)
	}
}

func TestResumeAfterRemoteStepDoesNotRepeatAppliedStep(t *testing.T) {
	ctx, e, p := flowFixture(t)
	var calls int
	e.Apply = func(ctx context.Context, x *Execution) error {
		if err := x.RemoteStep(
			ctx,
			"change",
			func(context.Context) (any, error) { calls++; return true, nil },
			nil,
		); err != nil {
			return err
		}
		if x.Case.Attempts == 1 {
			return errors.New("temporary downstream failure")
		}
		return nil
	}
	first, err := e.Execute(ctx, executeRequest(p))
	if err != nil || first.Correction.Status != "retrying" {
		t.Fatalf("first: %v %v", first, err)
	}
	second, err := e.Execute(ctx, executeRequest(p))
	if err != nil || second.Correction.Status != "completed" || calls != 1 {
		t.Fatalf("resume: %v %v calls=%d", second, err, calls)
	}
}

func TestStaleLeaseCannotReleaseNewExecutorsReservation(t *testing.T) {
	ctx, e, p := flowFixture(t)
	c, err := e.load(ctx, p.PreviewId)
	if err != nil {
		t.Fatal(err)
	}
	c.LeaseToken = "old-executor"
	if _, err = e.DB.ExecContext(
		ctx,
		`UPDATE payment.admin_correction SET status='executing',reserved=true,lease_token='new-executor',lease_until=now()+interval '90 seconds' WHERE id=?`,
		c.ID,
	); err != nil {
		t.Fatal(err)
	}
	called := false
	e.RollbackReserve = func(context.Context, bun.Tx, *Case) error { called = true; return nil }
	if err = e.releaseResources(ctx, c); connect.CodeOf(err) != connect.CodeAborted || called {
		t.Fatalf("stale release: %v callback=%v", err, called)
	}
	c.LeaseToken = "new-executor"
	if err = e.releaseResources(ctx, c); err != nil || !called {
		t.Fatalf("current release: %v callback=%v", err, called)
	}
	loaded, err := e.load(ctx, c.ID)
	if err != nil || loaded.Reserved || loaded.Status != "failed" {
		t.Fatalf("state: %v %v", loaded, err)
	}
}

func TestRevokedAdministratorCannotResumeAndExpiredPreviewCannotExecute(t *testing.T) {
	ctx, e, p := flowFixture(t)
	if _, err := e.DB.ExecContext(
		ctx,
		`UPDATE payment.admin_correction SET expires_at=? WHERE id=?`,
		time.Now().Add(-time.Minute),
		p.PreviewId,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(ctx, executeRequest(p)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("expired preview: %v", err)
	}
	if _, err := e.DB.ExecContext(
		ctx,
		`UPDATE "user".user_account SET auth_version=auth_version+1 WHERE id=42`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(ctx, executeRequest(p)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("revoked session: %v", err)
	}
}

func TestUnknownReservationCommitRemainsResumable(t *testing.T) {
	ctx, e, p := flowFixture(t)
	c, err := e.load(ctx, p.PreviewId)
	if err != nil {
		t.Fatal(err)
	}
	c.LeaseToken = "unknown-commit-lease"
	if _, err = e.DB.ExecContext(
		ctx,
		`UPDATE payment.admin_correction SET status='executing',reserved=true,request_id=?,lease_token=?,lease_until=now()+interval '90 seconds' WHERE id=?`,
		executeRequest(p).RequestId,
		c.LeaseToken,
		c.ID,
	); err != nil {
		t.Fatal(err)
	}
	if err = e.recordReserveError(ctx, c, errors.New("connection lost during commit")); err != nil {
		t.Fatal(err)
	}
	c, err = e.load(ctx, c.ID)
	if err != nil || c.Status != "retrying" || !c.Reserved {
		t.Fatalf("unknown committed reservation stranded: %v %v", c, err)
	}
	calls := 0
	e.Reserve = func(context.Context, bun.Tx, *Case) error { calls++; return nil }
	result, err := e.Execute(ctx, executeRequest(p))
	if err != nil || result.Correction.Status != "completed" || calls != 0 {
		t.Fatalf("reservation repeated: %v %v calls=%d", result, err, calls)
	}
}
