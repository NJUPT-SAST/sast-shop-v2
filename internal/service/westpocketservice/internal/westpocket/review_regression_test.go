package westpocket

import (
	"context"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
)

func TestPostgresFaceSelectionRespectsResolutionAndRetention(t *testing.T) {
	s := integrationService(t)
	ctx := context.Background()
	id := mustCreate(t, s, 10)
	u, _, err := s.Upload(ctx, 10, id, "group_photo", "photo-v1", randomUUID(), testPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddPhotos(ctx, 10, id, 1, []int64{u.ID}, "temporary", "photo-v1", randomUUID()); err != nil {
		t.Fatal(err)
	}
	photos, err := s.Photos(ctx, 10, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartRecognition(ctx, 10, id, []int64{photos[0].ID}, randomUUID()); err != nil {
		t.Fatal(err)
	}
	if err = s.runNextJob(ctx); err != nil {
		t.Fatal(err)
	}
	matches, err := s.Matches(ctx, 10, id, 0)
	if err != nil || len(matches) != 1 {
		t.Fatalf("matches: %v %v", matches, err)
	}
	matchID := matches[0].ID
	_, err = s.DB.ExecContext(ctx,
		"UPDATE westpocket.face_match SET suggested_user_id=20,confirmed_user_id=30,resolution='confirmed' WHERE id=?",
		matchID)
	requireReviewSuccess(t, err)
	members := []Member{
		{UserID: 10, SelectionSource: "owner"},
		{UserID: 20, SelectionSource: "face", FaceMatchID: &matchID},
	}
	if _, err = s.ReplaceMembers(
		ctx,
		10,
		id,
		2,
		members,
		randomUUID(),
	); connect.CodeOf(
		err,
	) != connect.CodeInvalidArgument {
		t.Fatalf("overridden suggestion accepted: %v", err)
	}
	_, err = s.DB.ExecContext(ctx,
		"UPDATE westpocket.face_match SET confirmed_user_id=NULL,resolution='pending' WHERE id=?", matchID)
	requireReviewSuccess(t, err)
	if _, err = s.ReplaceMembers(
		ctx,
		10,
		id,
		2,
		members,
		randomUUID(),
	); connect.CodeOf(
		err,
	) != connect.CodeInvalidArgument {
		t.Fatalf("suggestion without current face consent accepted: %v", err)
	}
	_, err = s.DB.ExecContext(ctx,
		"UPDATE westpocket.face_match SET confirmed_user_id=30,resolution='confirmed' WHERE id=?", matchID)
	requireReviewSuccess(t, err)
	members[1].UserID = 30
	if _, err = s.ReplaceMembers(ctx, 10, id, 2, members, randomUUID()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Resolve(ctx, 10, id, matchID, 0, 3, true, randomUUID()); err != nil {
		t.Fatal(err)
	}
	members[1].UserID = 20
	if _, err = s.ReplaceMembers(
		ctx,
		10,
		id,
		4,
		members,
		randomUUID(),
	); connect.CodeOf(
		err,
	) != connect.CodeInvalidArgument {
		t.Fatalf("ignored face accepted: %v", err)
	}
	_, err = s.DB.ExecContext(ctx,
		"UPDATE westpocket.pocket_photo SET retention_until=now()-interval '1 second' WHERE id=?", photos[0].ID)
	requireReviewSuccess(t, err)
	if _, err = s.Resolve(ctx, 10, id, matchID, 20, 4, false, randomUUID()); err == nil {
		t.Fatal("expired photo could be resolved")
	}
	if _, err = s.ReplaceMembers(
		ctx,
		10,
		id,
		4,
		members,
		randomUUID(),
	); connect.CodeOf(
		err,
	) != connect.CodeInvalidArgument {
		t.Fatalf("expired photo could select member: %v", err)
	}
}

func requireReviewSuccess(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestPostgresRenewedEnrollmentSurvivesPurge(t *testing.T) {
	s := integrationService(t)
	ctx := context.Background()
	p := Profile{
		UserID:            40,
		Status:            "revoked",
		Revision:          3,
		EnrollmentVersion: 2,
		ConsentVersion:    "old",
		ConsentedAt:       time.Now().Add(-366 * 24 * time.Hour),
		ConsentExpiresAt:  time.Now().Add(-time.Hour),
	}
	if _, err := s.DB.NewInsert().Model(&p).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	u, _, err := s.Upload(ctx, 40, 0, "face_sample", "face-v1", randomUUID(), testPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Enroll(ctx, 40, 0, []int64{u.ID}, "face-v1", randomUUID(), false); err != nil {
		t.Fatal(err)
	}
	if err = s.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	current := mustProfile(t, s, 40)
	if current.Status != "pending" || current.ConsentVersion != "face-v1" ||
		current.ConsentExpiresAt.Before(time.Now()) {
		t.Fatalf("renewed consent revoked while awaiting enrollment: %+v", current)
	}
	if err = s.runNextJob(ctx); err != nil {
		t.Fatal(err)
	}
	if current = mustProfile(t, s, 40); current.Status != "active" {
		t.Fatalf("renewed enrollment did not activate: %+v", current)
	}
}

func TestPostgresReconcileVisitsBeyondFirstBatch(t *testing.T) {
	s := integrationService(t)
	ctx := context.Background()
	payments := mustType[*fakePayments](t, s.Payments)
	var last int64
	for i := 0; i < 51; i++ {
		id := mustCreate(t, s, 10)
		if _, err := s.DB.ExecContext(
			ctx,
			"UPDATE westpocket.pocket SET status='collecting' WHERE id=?",
			id,
		); err != nil {
			t.Fatal(err)
		}
		bill, err := payments.Create(ctx, id, 20, 10, 10000)
		if err != nil {
			t.Fatal(err)
		}
		if i == 50 {
			bill.Status = "completed"
			payments.bills[fmt.Sprintf("%d:%d", id, 20)] = bill
			last = id
		}
		member := Member{
			PocketID:        id,
			UserID:          20,
			SelectionSource: "search",
			ShareCents:      10000,
			PaymentBillID:   &bill.ID,
			BillStatus:      "unpaid",
			AlbumAccess:     "pending",
		}
		if _, err = s.DB.NewInsert().Model(&member).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if p := mustPocket(t, s, last); p.Status != "settled" {
		t.Fatalf("later pocket starved by unpaid first batch: %+v", p)
	}
}

func TestPostgresUploadCompletesWithOneConnection(t *testing.T) {
	s := integrationService(t)
	s.DB.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	u, _, err := s.Upload(ctx, 40, 0, "face_sample", "face-v1", randomUUID(), testPNG(t))
	if err != nil {
		t.Fatalf("upload deadlocked while holding its only connection: %v", err)
	}
	if err = s.deleteUpload(ctx, u.ID); err != nil {
		t.Fatalf("deletion deadlocked while holding its only connection: %v", err)
	}
}
