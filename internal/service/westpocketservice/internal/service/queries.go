package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice/internal/repository"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice/internal/support"
	"github.com/uptrace/bun"
)

func (s *Service) cursor(actor, id int64, scope string) string {
	data := fmt.Sprintf("%d:%d:%s", actor, id, scope)
	mac := hmac.New(sha256.New, s.CursorKey)
	_, _ = mac.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString([]byte(data)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) parseCursor(token string, actor int64, scope string) (int64, error) {
	if token == "" {
		return 0, nil
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return 0, support.Failure(connect.CodeInvalidArgument, "分页标记无效")
	}
	data, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return 0, support.Failure(connect.CodeInvalidArgument, "分页标记无效")
	}
	signature, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return 0, support.Failure(connect.CodeInvalidArgument, "分页标记无效")
	}
	mac := hmac.New(sha256.New, s.CursorKey)
	_, _ = mac.Write(data)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return 0, support.Failure(connect.CodeInvalidArgument, "分页标记无效")
	}
	values := strings.SplitN(string(data), ":", 3)
	if len(values) != 3 || values[0] != strconv.FormatInt(actor, 10) || values[2] != scope {
		return 0, support.Failure(connect.CodeInvalidArgument, "分页标记不匹配")
	}
	id, e := strconv.ParseInt(values[1], 10, 64)
	if e != nil || id <= 0 {
		return 0, support.Failure(connect.CodeInvalidArgument, "分页标记无效")
	}
	return id, nil
}

func (s *Service) List(
	ctx context.Context,
	actor int64,
	perspective, status string,
	size int,
	token string,
) ([]model.Pocket, string, error) {
	if perspective == "" {
		perspective = "owner"
	}
	if perspective != "owner" && perspective != "member" {
		return nil, "", support.Failure(connect.CodeInvalidArgument, "活动视角无效")
	}
	if size <= 0 || size > 20 {
		size = 20
	}
	scope := "list:" + perspective + ":" + status
	after, e := s.parseCursor(token, actor, scope)
	if e != nil {
		return nil, "", e
	}
	pockets, e := repository.ListPockets(ctx, s.DB, actor, perspective == "owner", status, after, size+1)
	if e != nil {
		return nil, "", e
	}
	next := ""
	if len(pockets) > size {
		pockets = pockets[:size]
		next = s.cursor(actor, pockets[len(pockets)-1].ID, scope)
	}
	return pockets, next, nil
}

func (s *Service) Photos(ctx context.Context, actor, id int64, album bool) ([]model.Photo, error) {
	p, m, e := s.Pocket(ctx, actor, id)
	if e != nil {
		return nil, e
	}
	if p.OwnerID != actor {
		if !album {
			return nil, nil
		}
		accepted := false
		for _, a := range m {
			accepted = accepted || a.UserID == actor && a.AlbumAccess == "accepted"
		}
		if !accepted {
			return nil, ErrDenied
		}
		declined, e := repository.CountPendingAlbumMembers(ctx, s.DB, id)
		if e != nil {
			return nil, e
		}
		if declined > 0 {
			return nil, support.Failure(connect.CodeFailedPrecondition, "成员尚未全部同意相册分享")
		}
	}
	return repository.ListPhotos(ctx, s.DB, id, album)
}

func (s *Service) SetAlbum(ctx context.Context, actor, id int64, accepted bool, requestID string) error {
	_, e := s.mutate(ctx, actor, "album", requestID, []any{id, accepted}, func(tx bun.Tx) (int64, error) {
		if e := lockAlbumPocket(ctx, tx, id); e != nil {
			return 0, e
		}
		m := new(model.Member)
		if e := tx.NewSelect().
			Model(m).
			Where("pocket_id=? AND user_id=?", id, actor).
			For("UPDATE").
			Scan(ctx); e != nil {
			return 0, ErrDenied
		}
		m.AlbumAccess = "declined"
		if accepted {
			m.AlbumAccess = "accepted"
		}
		if _, e := tx.NewUpdate().Model(m).Column("album_access").WherePK().Exec(ctx); e != nil {
			return 0, e
		}
		action := "revoke"
		if accepted {
			action = "grant"
		}
		_, e := tx.ExecContext(
			ctx,
			"INSERT INTO westpocket.consent_event(user_id,purpose,action,policy_version,pocket_id) VALUES (?,?,?,?,?)",
			actor,
			"album_access",
			action,
			s.PhotoPolicy,
			id,
		)
		return id, e
	})
	return e
}

func (s *Service) Remind(ctx context.Context, actor, id int64, memberIDs []int64, requestID string) error {
	p, _, e := s.Pocket(ctx, actor, id)
	if e != nil {
		return e
	}
	if p.OwnerID != actor {
		return ErrDenied
	}
	if e := s.Refresh(ctx, id); e != nil {
		return e
	}
	_, e = s.mutate(ctx, actor, "remind", requestID, []any{id, memberIDs}, func(tx bun.Tx) (int64, error) {
		p, e := pocketTx(ctx, tx, actor, id, 0, false)
		if e != nil {
			return 0, e
		}
		if p.Status != "collecting" {
			return 0, ErrState
		}
		m, e := repository.ListMembers(ctx, tx, id)
		if e != nil {
			return 0, e
		}
		requested := map[int64]bool{}
		for _, v := range memberIDs {
			requested[v] = true
		}
		matched := 0
		for _, a := range m {
			if !reminderEligible(a, actor, requested) {
				continue
			}
			matched++

			if e = s.remindMember(ctx, tx, id, &a); e != nil {
				return 0, e
			}

		}
		if matched == 0 {
			return 0, support.Failure(connect.CodeFailedPrecondition, "没有可提醒的待付款成员")
		}
		if _, e = tx.ExecContext(
			ctx,
			"UPDATE westpocket.notification_outbox SET status='pending',attempt_count=0,next_attempt_at=now(),updated_at=now() WHERE pocket_id=? AND recipient_user_id=? AND kind='summary' AND lifecycle_round=? AND status='failed'",
			id,
			actor,
			p.LifecycleRound,
		); e != nil {
			return 0, e
		}
		return id, nil
	})
	return e
}

func (s *Service) remindMember(ctx context.Context, tx bun.Tx, id int64, a *model.Member) error {
	if a.LastRemindedAt != nil && time.Since(*a.LastRemindedAt) < 30*time.Minute {
		return support.Failure(connect.CodeResourceExhausted, "同一成员每 30 分钟可提醒一次")
	}
	result, e := tx.ExecContext(
		ctx,
		"UPDATE westpocket.notification_outbox SET status='pending',attempt_count=0,next_attempt_at=now(),updated_at=now() WHERE pocket_id=? AND recipient_user_id=? AND lifecycle_round=? AND status='failed'",
		id,
		a.UserID,
		requestedRound(ctx),
	)
	if e != nil {
		return e
	}
	count, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if count == 0 {
		var sequence int
		if e = tx.NewRaw("SELECT COALESCE(MAX(sequence),0)+1 FROM westpocket.notification_outbox WHERE pocket_id=? AND recipient_user_id=? AND kind='reminder' AND lifecycle_round=?", id, a.UserID, requestedRound(ctx)).
			Scan(ctx, &sequence); e != nil {
			return e
		}
		if e = enqueueNotification(ctx, tx, id, a.UserID, "reminder", sequence); e != nil {
			return e
		}
	}
	_, e = tx.ExecContext(ctx, "UPDATE westpocket.pocket_member SET last_reminded_at=now() WHERE id=?", a.ID)
	return e
}

func (s *Service) Notifications(ctx context.Context, actor, id int64) ([]model.Notification, error) {
	p, _, e := s.Pocket(ctx, actor, id)
	if e != nil {
		return nil, e
	}
	return repository.ListNotifications(ctx, s.DB, id, p.LifecycleRound, actor, p.OwnerID == actor)
}

func (s *Service) Ready(ctx context.Context) (bool, error) {
	return repository.Ready(ctx, s.DB)
}

func (s *Service) CollectionState(
	ctx context.Context,
	id int64,
	checkAlipay bool,
) (*model.Pocket, []model.Member, bool, error) {
	p, members, err := s.internalPocket(ctx, id)
	if err != nil {
		return nil, nil, false, err
	}
	if p.AdminOperationID != "" {
		return nil, nil, false, support.Failure(connect.CodeFailedPrecondition, "活动正在纠错")
	}
	alipayAvailable := false
	if checkAlipay && p.Status == "collecting" {
		qr, err := s.readQRSnapshot(p)
		if err != nil {
			return nil, nil, false, err
		}
		alipayAvailable = qr.AlipayContent != ""
	}
	return p, members, alipayAvailable, nil
}

func (s *Service) ProfileWithLatestJob(ctx context.Context, actor int64) (*model.Profile, int, int64, error) {
	profile, count, err := s.Profile(ctx, actor)
	if err != nil || profile == nil {
		return profile, count, 0, err
	}
	job, err := repository.LatestProfileJob(ctx, s.DB, profile.ID)
	if err != nil {
		return profile, count, 0, nil
	}
	return profile, count, job.ID, nil
}

func (s *Service) RecentPocketJobs(ctx context.Context, actor int64, pocket *model.Pocket) ([]model.Job, error) {
	if pocket.OwnerID != actor {
		return nil, nil
	}
	return repository.ListPocketJobs(ctx, s.DB, pocket.ID, 20)
}

func (s *Service) ListAlbumPhotos(
	ctx context.Context,
	actor, id int64,
	size int,
	token string,
) ([]model.Photo, string, error) {
	photos, err := s.Photos(ctx, actor, id, true)
	if err != nil {
		return nil, "", err
	}
	scope := "album:" + strconv.FormatInt(id, 10)
	after, err := s.parseCursor(token, actor, scope)
	if err != nil {
		return nil, "", err
	}
	filtered := []model.Photo{}
	for _, photo := range photos {
		if photo.ID > after {
			filtered = append(filtered, photo)
		}
	}
	if size <= 0 || size > 20 {
		size = 20
	}
	next := ""
	if len(filtered) > size {
		filtered = filtered[:size]
		next = s.cursor(actor, filtered[len(filtered)-1].ID, scope)
	}
	return filtered, next, nil
}

func (s *Service) Snapshot(ctx context.Context, actor, id int64) (*model.Pocket, *Bill, QR, error) {
	p, m, e := s.Pocket(ctx, actor, id)
	if e != nil {
		return nil, nil, QR{}, e
	}
	var qr QR
	if collectionReadable(p.Status) {
		if qr, e = s.readQRSnapshot(p); e != nil {
			return nil, nil, QR{}, e
		}
	}
	if p.OwnerID == actor {
		return p, nil, qr, nil
	}
	for _, a := range m {
		if a.UserID == actor && a.PaymentBillID != nil {
			bills, e := s.Payments.Bills(ctx, []int64{*a.PaymentBillID})
			if e != nil {
				return nil, nil, QR{}, e
			}
			if len(bills) != 1 || !matchesBill(bills[0], p, a) {
				return nil, nil, QR{}, ErrState
			}
			return p, &bills[0], qr, nil
		}
	}
	return p, nil, qr, nil
}

func (s *Service) readQRSnapshot(p *model.Pocket) (QR, error) {
	if s.Vault == nil {
		return QR{}, ErrUnavailable
	}
	data, err := s.Vault.Decrypt(p.QrCiphertext, p.ID)
	if err != nil {
		return QR{}, err
	}
	var qr QR
	if err := json.Unmarshal(data, &qr); err != nil {
		return QR{}, err
	}
	return qr, nil
}

func reminderEligible(m model.Member, owner int64, requested map[int64]bool) bool {
	return m.UserID != owner && m.BillStatus == "unpaid" && (len(requested) == 0 || requested[m.ID])
}

func matchesBill(b Bill, p *model.Pocket, m model.Member) bool {
	return b.PayerID == m.UserID && b.PayeeID == p.OwnerID && b.AmountCents == m.ShareCents && b.SourceID == p.ID &&
		b.SourceType == "west_pocket" && normalRound(b.LifecycleRound) == normalRound(p.LifecycleRound)
}
