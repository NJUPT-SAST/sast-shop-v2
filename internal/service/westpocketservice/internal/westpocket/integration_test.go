package westpocket

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

type fakeDirectory struct{}

func (fakeDirectory) GetUsers(_ context.Context, ids []int64) ([]User, error) {
	out := []User{}
	for _, id := range ids {
		if id > 0 && id < 1000 {
			out = append(out, User{ID: id, Name: fmt.Sprintf("user %d", id)})
		}
	}
	return out, nil
}

func (fakeDirectory) Search(context.Context, string, int, string) ([]User, string, error) {
	return []User{{ID: 20, Name: "user 20"}}, "", nil
}
func (fakeDirectory) OpenID(context.Context, int64) (string, error) { return "test-open-id", nil }

type fakePayments struct {
	mu        sync.Mutex
	bills     map[string]Bill
	failPayer int64
	next      int64
}

func (p *fakePayments) QR(context.Context, int64) (QR, error) {
	return QR{ID: 1, Content: "wxp://test-qr", UpdatedAt: time.Now()}, nil
}

func (p *fakePayments) Create(_ context.Context, id, payer, payee int64, amount int32) (Bill, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if payer == p.failPayer {
		return Bill{}, ErrUnavailable
	}
	key := fmt.Sprintf("%d:%d", id, payer)
	if b, ok := p.bills[key]; ok {
		return b, nil
	}
	p.next++
	b := Bill{
		ID:          p.next,
		PayerID:     payer,
		PayeeID:     payee,
		SourceID:    id,
		AmountCents: amount,
		SourceType:  "west_pocket",
		Status:      "unpaid",
		BillNo:      "test",
		VerifyCode:  "1234",
		UpdatedAt:   time.Now(),
	}
	p.bills[key] = b
	return b, nil
}

func (p *fakePayments) Bills(_ context.Context, ids []int64) ([]Bill, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []Bill{}
	for _, id := range ids {
		for _, b := range p.bills {
			if b.ID == id {
				out = append(out, b)
			}
		}
	}
	return out, nil
}

func (p *fakePayments) Cancel(_ context.Context, id int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, b := range p.bills {
		if b.SourceID == id && (b.Status == "submitted" || b.Status == "completed") {
			return failure(connect.CodeFailedPrecondition, "payment already submitted")
		}
	}
	for key, b := range p.bills {
		if b.SourceID == id {
			b.Status = "closed"
			p.bills[key] = b
		}
	}
	return nil
}

type fakeStorage struct{ data map[string][]byte }

func (s *fakeStorage) Put(_ context.Context, key string, data []byte) error {
	s.data[key] = append([]byte(nil), data...)
	return nil
}

func (s *fakeStorage) Read(_ context.Context, key string) ([]byte, error) {
	data, ok := s.data[key]
	if !ok {
		return nil, errors.New("missing")
	}
	return data, nil
}

func (s *fakeStorage) Delete(_ context.Context, key string) error { delete(s.data, key); return nil }

func (s *fakeStorage) URL(_ context.Context, key string) (string, error) {
	return "https://private.invalid/" + key, nil
}

type fakeFaces struct {
	enroll    func()
	recognize func()
	deleted   []string
}

func (f *fakeFaces) Enroll(context.Context, string, [][]byte) error {
	if f.enroll != nil {
		f.enroll()
	}
	return nil
}

func (f *fakeFaces) Delete(_ context.Context, person string) error {
	f.deleted = append(f.deleted, person)
	return nil
}

func (f *fakeFaces) Recognize(context.Context, []byte) ([]Face, error) {
	if f.recognize != nil {
		f.recognize()
	}
	return []Face{{X: 10, Y: 10, Width: 50, Height: 50}}, nil
}

type fakeMessenger struct {
	calls int
	fail  bool
	uuids []string
	links []string
}

func (f *fakeMessenger) Send(_ context.Context, _, uuid, _, link string, _ []byte) (string, error) {
	f.calls++
	f.uuids = append(f.uuids, uuid)
	f.links = append(f.links, link)
	if f.fail {
		return "", ErrUnavailable
	}
	return "message-id", nil
}

func TestPostgresNotificationLinksUsePocketRoutes(t *testing.T) {
	s := integrationService(t)
	ctx := context.Background()
	id := mustCreate(t, s, 10)
	mustMembers(t, s, id)
	if _, err := s.Publish(ctx, 10, id, 2, randomUUID()); err != nil {
		t.Fatal(err)
	}
	if err := s.runNextJob(ctx); err != nil {
		t.Fatal(err)
	}
	messenger := mustType[*fakeMessenger](t, s.Messenger)
	for _, mobileURL := range []string{"https://shop.sast.fun", "https://shop.sast.fun/"} {
		for _, kind := range []string{"summary", "payment", "payment_qr"} {
			t.Run(mobileURL+"/"+kind, func(t *testing.T) {
				s.MobileURL = mobileURL
				notice := &Notification{
					PocketID:        id,
					RecipientUserID: 20,
					MessageUUID:     randomUUID(),
					Kind:            kind,
				}
				if _, err := s.sendNotification(ctx, notice); err != nil {
					t.Fatal(err)
				}
				want := fmt.Sprintf("https://shop.sast.fun/pocket/%d", id)
				if kind != "summary" {
					want += "/pay"
				}
				if got := messenger.links[len(messenger.links)-1]; got != want {
					t.Fatalf("notification link = %q, want %q", got, want)
				}
			})
		}
	}
}

func integrationService(t *testing.T) *Service {
	t.Helper()
	dsn := os.Getenv("WEST_POCKET_TEST_DSN")
	if dsn == "" {
		t.Skip("set WEST_POCKET_TEST_DSN to a disposable PostgreSQL cluster with CREATE DATABASE privilege")
	}
	ctx := context.Background()
	admin := bun.NewDB(
		sql.OpenDB(
			pgdriver.NewConnector(
				pgdriver.WithDSN(dsn),
				pgdriver.WithConnParams(map[string]any{"client_encoding": "UTF8"}),
			),
		),
		pgdialect.New(),
	)
	name := "west_pocket_test_" + strings.ReplaceAll(randomUUID(), "-", "")
	if _, e := admin.ExecContext(ctx, "CREATE DATABASE "+name+" ENCODING 'UTF8' TEMPLATE template0"); e != nil {
		t.Fatal(e)
	}
	db := bun.NewDB(
		sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn), pgdriver.WithDatabase(name))),
		pgdialect.New(),
	)
	t.Cleanup(func() {
		if e := db.Close(); e != nil {
			t.Error(e)
		}
		if _, e := admin.ExecContext(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); e != nil {
			t.Error(e)
		}
		if e := admin.Close(); e != nil {
			t.Error(e)
		}
	})
	migration, e := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "migrations", "002_west_pocket.sql"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.ExecContext(ctx, string(migration)); e != nil {
		t.Fatal(e)
	}
	vault, e := NewVault(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	if e != nil {
		t.Fatal(e)
	}
	return &Service{
		DB:          db,
		Directory:   fakeDirectory{},
		Payments:    &fakePayments{bills: map[string]Bill{}},
		Storage:     &fakeStorage{data: map[string][]byte{}},
		Faces:       &fakeFaces{},
		Messenger:   &fakeMessenger{},
		Vault:       vault,
		FacePolicy:  "face-v1",
		PhotoPolicy: "photo-v1",
		CursorKey:   []byte("test-cursor"),
		Threshold:   85,
		Margin:      5,
	}
}

func mustCreate(t *testing.T, s *Service, owner int64) int64 {
	t.Helper()
	id, e := s.Create(context.Background(), owner, "AA", 10000, randomUUID())
	if e != nil {
		t.Fatal(e)
	}
	return id
}

func mustMembers(t *testing.T, s *Service, id int64) {
	t.Helper()
	_, e := s.ReplaceMembers(
		context.Background(),
		10,
		id,
		1,
		[]Member{
			{UserID: 10, SelectionSource: "owner"},
			{UserID: 20, SelectionSource: "search"},
			{UserID: 30, SelectionSource: "search"},
		},
		randomUUID(),
	)
	if e != nil {
		t.Fatal(e)
	}
}

//nolint:gocyclo // Sequential integration scenarios share one isolated database and exercise its durable transitions.
func TestPostgresWorkflow(t *testing.T) {
	s := integrationService(t)
	ctx := context.Background()
	payments := mustType[*fakePayments](t, s.Payments)
	t.Run("concurrent_request_retries_and_revision", func(t *testing.T) {
		request := randomUUID()
		var wg sync.WaitGroup
		ids := make(chan int64, 8)
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				id, e := s.Create(ctx, 10, "concurrent", 10000, request)
				ids <- id
				errs <- e
			}()
		}
		wg.Wait()
		close(ids)
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		var prior int64
		for id := range ids {
			if prior != 0 && id != prior {
				t.Fatal("duplicate create")
			}
			prior = id
		}
		if _, e := s.Create(ctx, 10, "changed", 10000, request); connect.CodeOf(e) != connect.CodeAlreadyExists {
			t.Fatalf("different payload accepted: %v", e)
		}
		title := "updated"
		if _, e := s.Update(ctx, 10, prior, 1, &title, nil, randomUUID()); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Update(ctx, 10, prior, 1, &title, nil, randomUUID()); connect.CodeOf(e) != connect.CodeAborted {
			t.Fatalf("stale write accepted: %v", e)
		}
		if _, _, e := s.Pocket(ctx, 20, prior); connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("draft leaked")
		}
	})
	t.Run("durable_partial_publish_and_notifications", func(t *testing.T) {
		id := mustCreate(t, s, 10)
		mustMembers(t, s, id)
		request := randomUUID()
		jobID, e := s.Publish(ctx, 10, id, 2, request)
		if e != nil {
			t.Fatal(e)
		}
		duplicate, e := s.Publish(ctx, 10, id, 2, request)
		if e != nil || duplicate != jobID {
			t.Fatalf("publish retry: %d %v", duplicate, e)
		}
		payments.failPayer = 30
		if e = s.runNextJob(ctx); e == nil {
			t.Fatal("expected temporary billing failure")
		}
		p, m, e := s.internalPocket(ctx, id)
		if e != nil || p.Status != "publishing" || p.OwnerShareCents != 3334 || m[1].PaymentBillID == nil {
			t.Fatalf("partial publish not durable: %+v %v", p, e)
		}
		if _, e = s.Update(
			ctx,
			10,
			id,
			p.Revision,
			nil,
			ptr(int32(100)),
			randomUUID(),
		); connect.CodeOf(
			e,
		) != connect.CodeFailedPrecondition {
			t.Fatal("published amounts mutated")
		}
		payments.failPayer = 0
		observeExec(s.DB.ExecContext(ctx, "UPDATE westpocket.async_job SET next_attempt_at=now() WHERE id=?", jobID))
		if e = s.runNextJob(ctx); e != nil {
			t.Fatal(e)
		}
		p, m, e = s.internalPocket(ctx, id)
		if e != nil || p.Status != "collecting" || m[0].PaymentBillID != nil || len(payments.bills) != 2 {
			t.Fatalf("publish recovery: %+v %v", p, e)
		}
		n, e := s.Notifications(ctx, 10, id)
		if e != nil || len(n) != 5 {
			t.Fatalf("outbox count %d %v", len(n), e)
		}
		messenger := mustType[*fakeMessenger](t, s.Messenger)
		messenger.fail = true
		if e = s.runNextNotification(ctx); e == nil {
			t.Fatal("send failure ignored")
		}
		observeExec(
			s.DB.ExecContext(
				ctx,
				"UPDATE westpocket.notification_outbox SET next_attempt_at=now() WHERE id=?",
				n[0].ID,
			),
		)
		messenger.fail = false
		if e = s.runNextNotification(ctx); e != nil {
			t.Fatal(e)
		}
		if len(messenger.uuids) != 2 || messenger.uuids[0] != messenger.uuids[1] {
			t.Fatal("retry lost message dedup UUID")
		}
		if _, e = s.DB.ExecContext(
			ctx,
			"UPDATE westpocket.notification_outbox SET status='failed' WHERE pocket_id=? AND recipient_user_id=20 AND kind='payment_qr'",
			id,
		); e != nil {
			t.Fatal(e)
		}
		if e = s.Remind(ctx, 10, id, []int64{m[1].ID}, randomUUID()); e != nil {
			t.Fatal(e)
		}
		notifications, e := s.Notifications(ctx, 10, id)
		if e != nil {
			t.Fatal(e)
		}
		for _, notice := range notifications {
			if notice.RecipientUserID == 20 && notice.Kind == "payment_qr" && notice.Status != "pending" {
				t.Fatal("failed QR notification was not requeued")
			}
		}
		if e = s.Remind(
			ctx,
			10,
			id,
			[]int64{m[1].ID},
			randomUUID(),
		); connect.CodeOf(
			e,
		) != connect.CodeResourceExhausted {
			t.Fatal("reminder cooldown not enforced")
		}
		for key, b := range payments.bills {
			if b.SourceID == id {
				b.Status = "completed"
				payments.bills[key] = b
			}
		}
		if e = s.Refresh(ctx, id); e != nil {
			t.Fatal(e)
		}
		p = mustPocket(t, s, id)
		if p.Status != "settled" {
			t.Fatal("completed bills not reconciled")
		}
	})
	t.Run("cancel_refuses_submitted_then_closes_all", func(t *testing.T) {
		id := mustCreate(t, s, 10)
		mustMembers(t, s, id)
		_, e := s.Publish(ctx, 10, id, 2, randomUUID())
		if e != nil {
			t.Fatal(e)
		}
		if e = s.runNextJob(ctx); e != nil {
			t.Fatal(e)
		}
		p := mustPocket(t, s, id)
		for key, b := range payments.bills {
			if b.SourceID == id && b.PayerID == 20 {
				b.Status = "submitted"
				payments.bills[key] = b
			}
		}
		if _, e = s.Cancel(ctx, 10, id, p.Revision, "", randomUUID()); e != nil {
			t.Fatal(e)
		}
		if e = s.runNextJob(ctx); connect.CodeOf(e) != connect.CodeFailedPrecondition {
			t.Fatalf("unsafe cancellation accepted: %v", e)
		}
		p = mustPocket(t, s, id)
		if p.Status != "collecting" {
			t.Fatal("failed cancellation stranded pocket")
		}
		for key, b := range payments.bills {
			if b.SourceID == id {
				b.Status = "unpaid"
				payments.bills[key] = b
			}
		}
		if _, e = s.Cancel(ctx, 10, id, p.Revision, "", randomUUID()); e != nil {
			t.Fatal(e)
		}
		if e = s.runNextJob(ctx); e != nil {
			t.Fatal(e)
		}
		p = mustPocket(t, s, id)
		if p.Status != "cancelled" {
			t.Fatal("not cancelled")
		}
		snap, bill, qr, e := s.Snapshot(ctx, 20, id)
		if e != nil || snap.Status != "cancelled" || bill.Status != "closed" || qr.Content != "" {
			t.Fatalf("cancelled payment exposed stale QR: %v", e)
		}
		for _, b := range payments.bills {
			if b.SourceID == id && b.Status != "closed" {
				t.Fatal("partial cancel")
			}
		}
	})
	t.Run("upload_ownership_and_late_recognition_deletion", func(t *testing.T) {
		id := mustCreate(t, s, 10)
		u, _, e := s.Upload(ctx, 10, id, "group_photo", "photo-v1", randomUUID(), testPNG(t))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.AddPhotos(
			ctx,
			20,
			id,
			1,
			[]int64{u.ID},
			"temporary",
			"photo-v1",
			randomUUID(),
		); connect.CodeOf(
			e,
		) != connect.CodePermissionDenied {
			t.Fatalf("cross-owner upload: %v", e)
		}
		if _, e = s.AddPhotos(ctx, 10, id, 1, []int64{u.ID}, "temporary", "photo-v1", randomUUID()); e != nil {
			t.Fatal(e)
		}
		photos, e := s.Photos(ctx, 10, id, false)
		if e != nil || len(photos) != 1 {
			t.Fatal(e)
		}
		photoID := photos[0].ID
		_, e = s.StartRecognition(ctx, 10, id, []int64{photoID}, randomUUID())
		if e != nil {
			t.Fatal(e)
		}
		faces := mustType[*fakeFaces](t, s.Faces)
		faces.recognize = func() {
			if _, e := s.DeletePhoto(ctx, 10, id, photoID, randomUUID()); e != nil {
				t.Error(e)
			}
		}
		if e = s.runNextJob(ctx); e != nil {
			t.Fatal(e)
		}
		faces.recognize = nil
		matches, e := s.Matches(ctx, 10, id, 0)
		if e != nil || len(matches) != 0 {
			t.Fatalf("late recognition resurrected deleted photo: %v", e)
		}
		if e = s.runNextJob(ctx); e != nil {
			t.Fatal(e)
		}
		if len(mustType[*fakeStorage](t, s.Storage).data) != 0 {
			t.Fatal("photo not removed from private storage")
		}
	})
	t.Run("inflight_enrollment_cannot_reactivate_revoked_profile", func(t *testing.T) {
		u, _, e := s.Upload(ctx, 40, 0, "face_sample", "face-v1", randomUUID(), testPNG(t))
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.Enroll(ctx, 40, 0, []int64{u.ID}, "face-v1", randomUUID(), false)
		if e != nil {
			t.Fatal(e)
		}
		faces := mustType[*fakeFaces](t, s.Faces)
		faces.enroll = func() {
			p, _, e := s.Profile(ctx, 40)
			if e != nil {
				t.Error(e)
				return
			}
			if _, e = s.Revoke(ctx, 40, p.Revision, randomUUID()); e != nil {
				t.Error(e)
			}
		}
		if e = s.runNextJob(ctx); e != nil {
			t.Fatal(e)
		}
		faces.enroll = nil
		p := mustProfile(t, s, 40)
		if p.Status != "revoking" || p.PersonID != "" {
			t.Fatal("revoked enrollment reactivated")
		}
		if e = s.runNextJob(ctx); e != nil {
			t.Fatal(e)
		}
		p = mustProfile(t, s, 40)
		if p.Status != "revoked" {
			t.Fatal("cloud deletion not completed")
		}
	})

	t.Run("recognition_rows_manual_resolution_and_member_authorization", func(t *testing.T) {
		id := mustCreate(t, s, 10)
		u, _, e := s.Upload(ctx, 10, id, "group_photo", "photo-v1", randomUUID(), testPNG(t))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.AddPhotos(ctx, 10, id, 1, []int64{u.ID}, "temporary", "photo-v1", randomUUID()); e != nil {
			t.Fatal(e)
		}
		photos, e := s.Photos(ctx, 10, id, false)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.StartRecognition(ctx, 10, id, []int64{photos[0].ID}, randomUUID()); e != nil {
			t.Fatal(e)
		}
		if e = s.runNextJob(ctx); e != nil {
			t.Fatal(e)
		}
		matches, e := s.Matches(ctx, 10, id, 0)
		if e != nil || len(matches) != 1 {
			t.Fatalf("recognition did not persist: %v", e)
		}
		if _, e = s.Matches(ctx, 20, id, 0); connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("private candidates leaked")
		}
		if _, e = s.Resolve(ctx, 10, id, matches[0].ID, 20, 2, false, randomUUID()); e != nil {
			t.Fatal(e)
		}
		if _, e = s.ReplaceMembers(
			ctx,
			10,
			id,
			3,
			[]Member{
				{UserID: 10, SelectionSource: "owner"},
				{UserID: 20, SelectionSource: "face", FaceMatchID: &matches[0].ID},
			},
			randomUUID(),
		); e != nil {
			t.Fatal(e)
		}
		if _, e = s.DeletePhoto(ctx, 10, id, photos[0].ID, randomUUID()); e != nil {
			t.Fatal(e)
		}
		if e = s.runNextJob(ctx); e != nil {
			t.Fatal(e)
		}
		_, members, e := s.internalPocket(ctx, id)
		if e != nil || len(members) != 2 || members[1].FaceMatchID != nil {
			t.Fatal("deletion removed member or retained biometric reference")
		}
	})
	t.Run("privacy_deletion_keeps_retrying_after_five_failures", func(t *testing.T) {
		now := time.Now()
		p := Profile{
			UserID:            70,
			Status:            "revoking",
			Revision:          2,
			EnrollmentVersion: 2,
			ConsentVersion:    "face-v1",
			ConsentedAt:       now,
			ConsentExpiresAt:  now.Add(time.Hour),
		}
		if _, e := s.DB.NewInsert().Model(&p).Returning("id").Exec(ctx); e != nil {
			t.Fatal(e)
		}
		j := Job{
			Kind:         "revoke",
			OwnerID:      70,
			ProfileID:    &p.ID,
			InputVersion: 2,
			Payload:      json.RawMessage(`{}`),
			Progress:     json.RawMessage(`{}`),
			DedupeKey:    "retry-delete",
			Status:       "queued",
			AttemptCount: 5,
		}
		if _, e := s.DB.NewInsert().Model(&j).Returning("id").Exec(ctx); e != nil {
			t.Fatal(e)
		}
		faces := s.Faces
		s.Faces = nil
		if e := s.runNextJob(ctx); e == nil {
			t.Fatal("missing provider incorrectly succeeded")
		}
		s.Faces = faces
		current, e := s.GetJob(ctx, 70, j.ID)
		if e != nil || current.Status != "queued" || current.AttemptCount != 6 {
			t.Fatal("privacy deletion stopped retrying")
		}
		if _, e = s.DB.ExecContext(
			ctx,
			"UPDATE westpocket.async_job SET next_attempt_at=now() WHERE id=?",
			j.ID,
		); e != nil {
			t.Fatal(e)
		}
		if e = s.runNextJob(ctx); e != nil {
			t.Fatal(e)
		}
		p2 := mustProfile(t, s, 70)
		if p2.Status != "revoked" {
			t.Fatal("provider recovery did not complete deletion")
		}
	})
	t.Run("obsolete_cleanup_preserves_current_active_person", func(t *testing.T) {
		now := time.Now()
		p := Profile{
			UserID:            50,
			PersonID:          "still-active",
			Status:            "active",
			Revision:          2,
			EnrollmentVersion: 2,
			ConsentVersion:    "face-v1",
			ConsentedAt:       now,
			ConsentExpiresAt:  now.Add(time.Hour),
		}
		if _, e := s.DB.NewInsert().Model(&p).Returning("id").Exec(ctx); e != nil {
			t.Fatal(e)
		}
		payload, e := json.Marshal(enrollPayload{PersonID: "still-active"})
		if e != nil {
			t.Fatal(e)
		}
		j := Job{ProfileID: &p.ID, InputVersion: 1, Payload: payload}
		faces := mustType[*fakeFaces](t, s.Faces)
		before := len(faces.deleted)
		if e := s.enrollJob(ctx, &j); e != nil {
			t.Fatal(e)
		}
		if len(faces.deleted) != before {
			t.Fatal("stale enrollment removed active fallback")
		}
		if e := s.revokeJob(ctx, &j); e != nil {
			t.Fatal(e)
		}
		if len(faces.deleted) != before {
			t.Fatal("stale revoke removed later enrollment")
		}
	})
}

func mustType[T any](t *testing.T, v any) T {
	t.Helper()
	typed, ok := v.(T)
	if !ok {
		t.Fatalf("unexpected test dependency %T", v)
	}
	return typed
}

func mustPocket(t *testing.T, s *Service, id int64) *Pocket {
	t.Helper()
	p, _, e := s.internalPocket(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func mustProfile(t *testing.T, s *Service, id int64) *Profile {
	t.Helper()
	p, _, e := s.Profile(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
