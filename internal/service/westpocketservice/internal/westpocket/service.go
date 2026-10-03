package westpocket

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/uptrace/bun"
)

type Directory interface {
	GetUsers(context.Context, []int64) ([]User, error)
	Search(context.Context, string, int, string) ([]User, string, error)
	OpenID(context.Context, int64) (string, error)
}
type Payments interface {
	QR(context.Context, int64) (QR, error)
	Create(context.Context, int64, int64, int64, int32) (Bill, error)
	Bills(context.Context, []int64) ([]Bill, error)
	Cancel(context.Context, int64) error
}
type Storage interface {
	Put(context.Context, string, []byte) error
	Read(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
	URL(context.Context, string) (string, error)
}
type Recognizer interface {
	Enroll(context.Context, string, [][]byte) error
	Delete(context.Context, string) error
	Recognize(context.Context, []byte) ([]Face, error)
}
type Messenger interface {
	Send(context.Context, string, string, string, string, []byte) (string, error)
}
type Service struct {
	DB                      *bun.DB
	Directory               Directory
	Payments                Payments
	Storage                 Storage
	Faces                   Recognizer
	Messenger               Messenger
	Vault                   *Vault
	CursorKey               []byte
	FacePolicy, PhotoPolicy string
	MobileURL               string
	Threshold, Margin       float64
}

func (s *Service) mutate(
	ctx context.Context,
	actor int64,
	method, requestID string,
	input any,
	fn func(bun.Tx) (int64, error),
) (int64, error) {
	if actor <= 0 {
		return 0, failure(connect.CodeUnauthenticated, "请先登录")
	}
	if !validUUID(requestID) {
		return 0, failure(connect.CodeInvalidArgument, "request_id 必须是 UUID")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return 0, err
	}
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	var result int64
	err = s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Serialize identical requests before reading the dedup record; all side effects stay in this transaction.
		if _, e := tx.ExecContext(
			ctx,
			"SELECT pg_advisory_xact_lock(hashtextextended(?,0))",
			fmt.Sprintf("wp:%d:%s:%s", actor, method, requestID),
		); e != nil {
			return e
		}
		var prior struct {
			RequestHash string
			ResourceID  int64
		}
		e := tx.NewRaw("SELECT request_hash,resource_id FROM westpocket.request_dedup WHERE actor_id=? AND rpc_method=? AND request_id=?", actor, method, requestID).
			Scan(ctx, &prior)
		if e == nil {
			if prior.RequestHash != hash {
				return failure(connect.CodeAlreadyExists, "此请求编号已用于不同参数")
			}
			result = prior.ResourceID
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		result, e = fn(tx)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(
			ctx,
			"INSERT INTO westpocket.request_dedup(actor_id,rpc_method,request_id,request_hash,resource_id) VALUES (?,?,?,?,?)",
			actor,
			method,
			requestID,
			hash,
			result,
		)
		return e
	})
	return result, err
}

func pocketTx(ctx context.Context, tx bun.Tx, actor, id, revision int64, draft bool) (*Pocket, error) {
	p := new(Pocket)
	err := tx.NewSelect().Model(p).Where("id = ?", id).For("UPDATE").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, failure(connect.CodeNotFound, "活动不存在")
	}
	if err != nil {
		return nil, err
	}
	if p.OwnerID != actor {
		return nil, ErrDenied
	}
	if revision > 0 && p.Revision != revision {
		return nil, ErrConflict
	}
	if draft && p.Status != "draft" {
		return nil, ErrState
	}
	return p, nil
}

func touch(ctx context.Context, tx bun.Tx, p *Pocket) error {
	p.Revision++
	p.UpdatedAt = time.Now()
	_, err := tx.NewUpdate().Model(p).WherePK().Exec(ctx)
	return err
}

func membersTx(ctx context.Context, db bun.IDB, id int64) ([]Member, error) {
	var m []Member
	err := db.NewSelect().Model(&m).Where("pocket_id = ?", id).OrderExpr("user_id ASC").Scan(ctx)
	return m, err
}

func queue(ctx context.Context, tx bun.Tx, j *Job) (int64, error) {
	j.Status = "queued"
	j.Progress = json.RawMessage(`{}`)
	if j.Payload == nil {
		j.Payload = json.RawMessage(`{}`)
	}
	_, err := tx.NewInsert().
		Model(j).
		On("CONFLICT (dedupe_key) DO UPDATE").
		Set("dedupe_key = EXCLUDED.dedupe_key").
		Returning("id").
		Exec(ctx)
	return j.ID, err
}

func (s *Service) Create(ctx context.Context, actor int64, title string, total int32, requestID string) (int64, error) {
	title, err := validateTitle(title)
	if err != nil {
		return 0, err
	}
	if total <= 0 {
		return 0, failure(connect.CodeInvalidArgument, "请输入大于零的金额")
	}
	return s.mutate(ctx, actor, "create", requestID, []any{title, total}, func(tx bun.Tx) (int64, error) {
		p := &Pocket{OwnerID: actor, Title: title, TotalCents: total, Status: "draft", Revision: 1, ParticipantCount: 1}
		if _, e := tx.NewInsert().Model(p).Returning("*").Exec(ctx); e != nil {
			return 0, e
		}
		m := &Member{PocketID: p.ID, UserID: actor, SelectionSource: "owner", AlbumAccess: "accepted"}
		_, e := tx.NewInsert().Model(m).Exec(ctx)
		return p.ID, e
	})
}

func (s *Service) Update(
	ctx context.Context,
	actor, id, revision int64,
	title *string,
	total *int32,
	requestID string,
) (int64, error) {
	if revision <= 0 {
		return 0, ErrConflict
	}
	if total != nil && *total <= 0 {
		return 0, failure(connect.CodeInvalidArgument, "金额必须大于零")
	}
	if title != nil {
		v, e := validateTitle(*title)
		if e != nil {
			return 0, e
		}
		title = &v
	}
	return s.mutate(ctx, actor, "update", requestID, []any{id, revision, title, total}, func(tx bun.Tx) (int64, error) {
		p, e := pocketTx(ctx, tx, actor, id, revision, true)
		if e != nil {
			return 0, e
		}
		if title != nil {
			p.Title = *title
		}
		if total != nil {
			p.TotalCents = *total
		}
		return id, touch(ctx, tx, p)
	})
}

func (s *Service) Pocket(ctx context.Context, actor, id int64) (*Pocket, []Member, error) {
	p := new(Pocket)
	e := s.DB.NewSelect().Model(p).Where("id = ?", id).Scan(ctx)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil, failure(connect.CodeNotFound, "活动不存在")
	}
	if e != nil {
		return nil, nil, e
	}
	m, e := membersTx(ctx, s.DB, id)
	if e != nil {
		return nil, nil, e
	}
	if p.OwnerID == actor {
		return p, m, nil
	}
	for _, a := range m {
		if a.UserID == actor && p.Status != "draft" {
			return p, []Member{a}, nil
		}
	}
	return nil, nil, ErrDenied
}

//nolint:gocyclo // Keep the transaction and its version/authorization checks together for auditability.
func (s *Service) ReplaceMembers(
	ctx context.Context,
	actor, id, revision int64,
	members []Member,
	requestID string,
) (int64, error) {
	if revision <= 0 {
		return 0, ErrConflict
	}
	if len(members) == 0 || len(members) > 50 {
		return 0, failure(connect.CodeInvalidArgument, "请选择 1–50 位成员")
	}
	ids := make([]int64, 0, len(members))
	seen := map[int64]bool{}
	for _, m := range members {
		if m.UserID <= 0 || seen[m.UserID] {
			return 0, failure(connect.CodeInvalidArgument, "成员无效或重复")
		}
		seen[m.UserID] = true
		ids = append(ids, m.UserID)
		if m.SelectionSource != "face" && m.SelectionSource != "search" && m.SelectionSource != "owner" {
			return 0, failure(connect.CodeInvalidArgument, "成员来源无效")
		}
	}
	users, err := s.Directory.GetUsers(ctx, ids)
	if err != nil {
		return 0, err
	}
	if len(users) != len(ids) {
		return 0, failure(connect.CodeInvalidArgument, "部分成员不存在或已停用")
	}
	return s.mutate(ctx, actor, "members", requestID, []any{id, revision, members}, func(tx bun.Tx) (int64, error) {
		p, e := pocketTx(ctx, tx, actor, id, revision, true)
		if e != nil {
			return 0, e
		}
		for _, m := range members {
			if m.FaceMatchID != nil {
				var match Match
				e = tx.NewSelect().
					Model(&match).
					Join("JOIN westpocket.pocket_photo ph ON ph.id=fm.photo_id").
					Where("fm.id=? AND ph.pocket_id=? AND ph.latest_job_id=fm.job_id AND ph.deleted_at IS NULL AND fm.expires_at>now()", *m.FaceMatchID, id).
					Scan(ctx)
				if e != nil {
					return 0, failure(connect.CodeInvalidArgument, "识别依据已失效")
				}
				if (match.ConfirmedUserID == nil || *match.ConfirmedUserID != m.UserID) &&
					(match.SuggestedUserID == nil || *match.SuggestedUserID != m.UserID) {
					return 0, failure(connect.CodeInvalidArgument, "识别依据与成员不一致")
				}
			}
		}
		if _, e = tx.NewDelete().Model((*Member)(nil)).Where("pocket_id=?", id).Exec(ctx); e != nil {
			return 0, e
		}
		for _, m := range members {
			v := Member{
				PocketID:        id,
				UserID:          m.UserID,
				SelectionSource: m.SelectionSource,
				FaceMatchID:     m.FaceMatchID,
				AlbumAccess:     "pending",
			}
			if m.UserID == actor {
				v.AlbumAccess = "accepted"
			}
			if _, e = tx.NewInsert().Model(&v).Exec(ctx); e != nil {
				return 0, e
			}
		}
		p.ParticipantCount = boundedInt32(len(members))
		return id, touch(ctx, tx, p)
	})
}

func (s *Service) Preview(ctx context.Context, actor, id, revision int64) (*Pocket, []Member, error) {
	p, m, e := s.Pocket(ctx, actor, id)
	if e != nil {
		return nil, nil, e
	}
	if p.OwnerID != actor {
		return nil, nil, ErrDenied
	}
	if revision <= 0 || p.Revision != revision {
		return nil, nil, ErrConflict
	}
	split, own, recv, e := Split(p.TotalCents, p.OwnerID, m)
	if e != nil {
		return nil, nil, e
	}
	p.OwnerShareCents = own
	p.ReceivableCents = recv
	p.ParticipantCount = boundedInt32(len(split))
	return p, split, nil
}

//nolint:gocyclo // Keep the transaction and its version/authorization checks together for auditability.
func (s *Service) Publish(ctx context.Context, actor, id, revision int64, requestID string) (int64, error) {
	if previous, ok, e := s.existingRequest(ctx, actor, "publish", requestID, []any{id, revision}); ok || e != nil {
		return previous, e
	}
	if revision <= 0 {
		return 0, ErrConflict
	}
	p, selected, e := s.Pocket(ctx, actor, id)
	if e != nil {
		return 0, e
	}
	if p.OwnerID != actor {
		return 0, ErrDenied
	}
	ids := make([]int64, 0, len(selected))
	for _, m := range selected {
		ids = append(ids, m.UserID)
	}
	users, e := s.Directory.GetUsers(ctx, ids)
	if e != nil {
		return 0, e
	}
	active := map[int64]bool{}
	for _, u := range users {
		active[u.ID] = true
	}
	for _, id := range ids {
		if !active[id] {
			return 0, failure(connect.CodeFailedPrecondition, "部分成员已停用，请更新名单")
		}
	}
	if s.Vault == nil {
		return 0, ErrUnavailable
	}
	// Snapshot is read before the transaction; only an authorized owner can request it.
	qr, e := s.Payments.QR(ctx, actor)
	if e != nil {
		return 0, e
	}
	if qr.Content == "" {
		return 0, failure(connect.CodeFailedPrecondition, "请先在个人主页添加微信收款码")
	}
	raw, e := json.Marshal(qr)
	if e != nil {
		return 0, e
	}
	encrypted, e := s.Vault.Encrypt(raw, id)
	if e != nil {
		return 0, e
	}
	return s.mutate(ctx, actor, "publish", requestID, []any{id, revision}, func(tx bun.Tx) (int64, error) {
		p, e := pocketTx(ctx, tx, actor, id, revision, true)
		if e != nil {
			return 0, e
		}
		members, e := membersTx(ctx, tx, id)
		if e != nil {
			return 0, e
		}
		split, own, recv, e := Split(p.TotalCents, actor, members)
		if e != nil {
			return 0, e
		}
		for i := range split {
			m := &split[i]
			if m.UserID == actor {
				m.BillStatus = "self_share"
			}
			if _, e = tx.NewUpdate().Model(m).Column("share_cents", "bill_status").WherePK().Exec(ctx); e != nil {
				return 0, e
			}
		}
		p.Status = "publishing"
		p.QrCiphertext = encrypted
		p.OwnerShareCents = own
		p.ReceivableCents = recv
		p.ParticipantCount = boundedInt32(len(split))
		if e = touch(ctx, tx, p); e != nil {
			return 0, e
		}
		return queue(
			ctx,
			tx,
			&Job{
				Kind:         "publish",
				OwnerID:      actor,
				PocketID:     &id,
				InputVersion: p.Revision,
				DedupeKey:    fmt.Sprintf("publish:%d", id),
			},
		)
	})
}

func (s *Service) Cancel(ctx context.Context, actor, id, revision int64, reason, requestID string) (int64, error) {
	if revision <= 0 {
		return 0, ErrConflict
	}
	if len([]rune(reason)) > 500 {
		return 0, failure(connect.CodeInvalidArgument, "取消原因过长")
	}
	return s.mutate(ctx, actor, "cancel", requestID, []any{id, revision, reason}, func(tx bun.Tx) (int64, error) {
		p, e := pocketTx(ctx, tx, actor, id, revision, false)
		if e != nil {
			return 0, e
		}
		if p.Status == "settled" || p.Status == "cancelled" || p.Status == "cancelling" {
			return 0, ErrState
		}
		p.CancelReason = reason
		if p.Status == "draft" {
			p.Status = "cancelled"
			now := time.Now()
			p.CancelledAt = &now
			return 0, touch(ctx, tx, p)
		}
		p.Status = "cancelling"
		if e = touch(ctx, tx, p); e != nil {
			return 0, e
		}
		return queue(
			ctx,
			tx,
			&Job{Kind: "cancel", OwnerID: actor, PocketID: &id, DedupeKey: fmt.Sprintf("cancel:%d:%d", id, p.Revision)},
		)
	})
}

func (s *Service) Search(
	ctx context.Context,
	actor, id int64,
	query string,
	size int,
	token string,
) ([]User, string, error) {
	p, _, e := s.Pocket(ctx, actor, id)
	if e != nil {
		return nil, "", e
	}
	if p.OwnerID != actor {
		return nil, "", ErrDenied
	}
	query = strings.TrimSpace(query)
	if len([]rune(query)) < 1 || len([]rune(query)) > 100 {
		return nil, "", failure(connect.CodeInvalidArgument, "请输入 1–100 字姓名")
	}
	if size <= 0 || size > 20 {
		size = 20
	}
	return s.Directory.Search(ctx, query, size, token)
}

func (s *Service) GetJob(ctx context.Context, actor, id int64) (*Job, error) {
	j := new(Job)
	e := s.DB.NewSelect().Model(j).Where("id=?", id).Scan(ctx)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, failure(connect.CodeNotFound, "任务不存在")
	}
	if e != nil {
		return nil, e
	}
	if j.OwnerID != actor {
		return nil, ErrDenied
	}
	return j, nil
}

func (s *Service) Retry(ctx context.Context, actor, id int64, requestID string) (int64, error) {
	return s.mutate(ctx, actor, "retry", requestID, id, func(tx bun.Tx) (int64, error) {
		j := new(Job)
		if e := tx.NewSelect().Model(j).Where("id=?", id).For("UPDATE").Scan(ctx); e != nil {
			return 0, e
		}
		if j.OwnerID != actor {
			return 0, ErrDenied
		}
		if j.Status != "failed" && j.Status != "partial_failed" {
			return 0, ErrState
		}
		if e := retryAllowed(ctx, tx, actor, j); e != nil {
			return 0, e
		}
		_, e := tx.NewUpdate().
			Model(j).
			Set("status='queued',attempt_count=0,error_code='',next_attempt_at=now(),lease_expires_at=NULL").
			WherePK().
			Exec(ctx)
		return id, e
	})
}

// existingRequest lets a publish retry retrieve its durable result even if a dependency is now offline.
func (s *Service) existingRequest(
	ctx context.Context,
	actor int64,
	method, requestID string,
	input any,
) (int64, bool, error) {
	if !validUUID(requestID) {
		return 0, false, failure(connect.CodeInvalidArgument, "request_id 必须是 UUID")
	}
	raw, e := json.Marshal(input)
	if e != nil {
		return 0, false, e
	}
	digest := sha256.Sum256(raw)
	var previous struct {
		RequestHash string
		ResourceID  int64
	}
	e = s.DB.NewRaw("SELECT request_hash,resource_id FROM westpocket.request_dedup WHERE actor_id=? AND rpc_method=? AND request_id=?", actor, method, requestID).
		Scan(ctx, &previous)
	if errors.Is(e, sql.ErrNoRows) {
		return 0, false, nil
	}
	if e != nil {
		return 0, false, e
	}
	if previous.RequestHash != hex.EncodeToString(digest[:]) {
		return 0, false, failure(connect.CodeAlreadyExists, "此请求编号已用于不同参数")
	}
	return previous.ResourceID, true, nil
}

func retryAllowed(ctx context.Context, tx bun.Tx, actor int64, j *Job) error {
	required := map[string]string{"recognize": "draft", "publish": "publishing", "cancel": "cancelling"}
	if expected, ok := required[j.Kind]; ok {
		if j.PocketID == nil {
			return ErrState
		}
		p, e := pocketTx(ctx, tx, actor, *j.PocketID, 0, false)
		if e != nil {
			return e
		}
		if p.Status != expected {
			return ErrState
		}
	}
	if j.Kind == "enroll" || j.Kind == "revoke" {
		if j.ProfileID == nil {
			return ErrState
		}
		p := new(Profile)
		if e := tx.NewSelect().Model(p).Where("id=?", *j.ProfileID).For("UPDATE").Scan(ctx); e != nil {
			return e
		}
		if p.EnrollmentVersion != j.InputVersion {
			return ErrState
		}
		if j.Kind == "enroll" && (p.Status == "revoking" || p.Status == "revoked") {
			return ErrState
		}
		if j.Kind == "revoke" && p.Status != "revoking" {
			return ErrState
		}
	}
	return nil
}
