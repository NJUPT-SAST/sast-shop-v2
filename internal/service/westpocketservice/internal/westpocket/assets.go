package westpocket

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"

	"github.com/uptrace/bun"
)

//nolint:gocyclo // Keep the transaction and its version/authorization checks together for auditability.
func (s *Service) Upload(
	ctx context.Context,
	actor, pocketID int64,
	purpose, consent, requestID string,
	data []byte,
) (*Upload, string, error) {
	if actor <= 0 {
		return nil, "", ErrDenied
	}
	if s.Storage == nil {
		return nil, "", ErrUnavailable
	}
	policy := s.PhotoPolicy
	if purpose == "face_sample" {
		policy = s.FacePolicy
	}
	if !validUUID(requestID) || consent != policy || policy == "" {
		return nil, "", failure(connect.CodeInvalidArgument, "请先阅读并同意当前版本的照片处理告知")
	}
	if purpose != "face_sample" && purpose != "group_photo" {
		return nil, "", failure(connect.CodeInvalidArgument, "图片用途无效")
	}
	if purpose == "face_sample" && s.Faces == nil {
		return nil, "", ErrUnavailable
	}
	normalized, width, height, err := NormalizeImage(data)
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256(normalized)
	digest := hex.EncodeToString(hash[:])
	u := new(Upload)
	err = s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, e := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(?)", -actor); e != nil {
			return e
		}
		e := tx.NewSelect().Model(u).Where("owner_id=? AND request_id=?", actor, requestID).Scan(ctx)
		if e == nil {
			if u.SHA256 != digest || u.Purpose != purpose || u.ConsentVersion != consent ||
				((u.PocketID == nil) != (pocketID == 0)) ||
				(u.PocketID != nil && *u.PocketID != pocketID) {
				return failure(connect.CodeAlreadyExists, "此上传编号已用于其他照片")
			}
			if u.DeletedAt != nil || u.Bound || time.Now().After(u.ExpiresAt) {
				return ErrState
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		count, e := tx.NewSelect().
			Model((*Upload)(nil)).
			Where("owner_id=? AND created_at>now()-interval '24 hours'", actor).
			Count(ctx)
		if e != nil {
			return e
		}
		if count >= 100 {
			return failure(connect.CodeResourceExhausted, "今日照片上传次数已达上限")
		}
		if purpose == "group_photo" {
			if _, e = pocketTx(ctx, tx, actor, pocketID, 0, true); e != nil {
				return e
			}
		} else if pocketID != 0 {
			return failure(connect.CodeInvalidArgument, "本人照片不能关联活动")
		}
		*u = Upload{
			OwnerID:        actor,
			Purpose:        purpose,
			ConsentVersion: consent,
			RequestID:      requestID,
			ObjectKey:      fmt.Sprintf("west-pocket/%d/%s.jpg", actor, requestID),
			SHA256:         digest,
			ByteSize:       int64(len(normalized)),
			Width:          width,
			Height:         height,
			ExpiresAt:      time.Now().Add(24 * time.Hour),
			Status:         "uploading",
		}
		if pocketID > 0 {
			u.PocketID = &pocketID
		}
		_, e = tx.NewInsert().Model(u).Returning("*").Exec(ctx)
		return e
	})
	if err != nil {
		return nil, "", err
	}
	err = s.withUploadLock(ctx, u.ID, func() error {
		if e := s.DB.NewSelect().Model(u).WherePK().Scan(ctx); e != nil {
			return e
		}
		if u.DeletedAt != nil || time.Now().After(u.ExpiresAt) {
			return ErrState
		}
		if u.Bound {
			return nil
		}
		if e := s.Storage.Put(ctx, u.ObjectKey, normalized); e != nil {
			return ErrUnavailable
		}
		u.Status = "ready"
		_, e := s.DB.NewUpdate().Model(u).Column("status").WherePK().Exec(ctx)
		return e
	})
	if err != nil {
		return nil, "", err
	}
	url, err := s.Storage.URL(ctx, u.ObjectKey)
	return u, url, err
}

func uploadTx(ctx context.Context, tx bun.Tx, actor, id int64, purpose string, pocketID int64) (*Upload, error) {
	u := new(Upload)
	e := tx.NewSelect().Model(u).Where("id=?", id).For("UPDATE").Scan(ctx)
	if e != nil {
		return nil, failure(connect.CodeInvalidArgument, "上传图片不存在")
	}
	if u.OwnerID != actor || u.Purpose != purpose || u.Bound || u.DeletedAt != nil || u.Status != "ready" ||
		time.Now().After(u.ExpiresAt) {
		return nil, ErrDenied
	}
	if purpose == "group_photo" && (u.PocketID == nil || *u.PocketID != pocketID) {
		return nil, ErrDenied
	}
	_, e = tx.NewUpdate().Model(u).Set("bound=true").WherePK().Exec(ctx)
	return u, e
}

func distinctIDs(ids []int64, max int) error {
	seen := map[int64]bool{}
	if len(ids) == 0 || len(ids) > max {
		return failure(connect.CodeInvalidArgument, "图片数量超出限制")
	}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return failure(connect.CodeInvalidArgument, "图片编号无效或重复")
		}
		seen[id] = true
	}
	return nil
}

//nolint:gocyclo // Keep the transaction and its version/authorization checks together for auditability.
func (s *Service) AddPhotos(
	ctx context.Context,
	actor, id, revision int64,
	uploads []int64,
	retention, consent, requestID string,
) (int64, error) {
	if s.Storage == nil {
		return 0, ErrUnavailable
	}
	if revision <= 0 {
		return 0, ErrConflict
	}
	if consent != s.PhotoPolicy || s.PhotoPolicy == "" {
		return 0, failure(connect.CodeInvalidArgument, "照片处理告知版本已更新")
	}
	if e := distinctIDs(uploads, 10); e != nil {
		return 0, e
	}
	if retention != "temporary" && retention != "keepsake" {
		return 0, failure(connect.CodeInvalidArgument, "保存方式无效")
	}
	return s.mutate(
		ctx,
		actor,
		"photos",
		requestID,
		[]any{id, revision, uploads, retention, consent},
		func(tx bun.Tx) (int64, error) {
			p, e := pocketTx(ctx, tx, actor, id, revision, true)
			if e != nil {
				return 0, e
			}
			count, e := tx.NewSelect().Model((*Photo)(nil)).Where("pocket_id=? AND deleted_at IS NULL", id).Count(ctx)
			if e != nil {
				return 0, e
			}
			if count+len(uploads) > 10 {
				return 0, failure(connect.CodeResourceExhausted, "每次活动最多 10 张合照")
			}
			for _, uploadID := range uploads {
				u, e := uploadTx(ctx, tx, actor, uploadID, "group_photo", id)
				if e != nil {
					return 0, e
				}
				until := time.Now().Add(24 * time.Hour)
				if retention == "keepsake" {
					until = time.Now().Add(30 * 24 * time.Hour)
				}
				ph := Photo{
					PocketID:             id,
					UploadID:             u.ID,
					UploaderID:           actor,
					ObjectKey:            u.ObjectKey,
					SHA256:               u.SHA256,
					Width:                u.Width,
					Height:               u.Height,
					Status:               "uploaded",
					RetentionMode:        retention,
					RetentionUntil:       until,
					AuthorizationVersion: consent,
				}
				if _, e = tx.NewInsert().Model(&ph).Returning("id").Exec(ctx); e != nil {
					return 0, e
				}
				_, e = tx.ExecContext(
					ctx,
					"INSERT INTO westpocket.consent_event(user_id,purpose,action,policy_version,pocket_id,photo_id) VALUES (?,?,?,?,?,?)",
					actor,
					"cloud_processing",
					"grant",
					consent,
					id,
					ph.ID,
				)
				if e != nil {
					return 0, e
				}
				if retention == "keepsake" {
					_, e = tx.ExecContext(
						ctx,
						"INSERT INTO westpocket.consent_event(user_id,purpose,action,policy_version,pocket_id,photo_id) VALUES (?,?,?,?,?,?)",
						actor,
						"photo_keepsake",
						"grant",
						consent,
						id,
						ph.ID,
					)
					if e != nil {
						return 0, e
					}
				}
			}
			return id, touch(ctx, tx, p)
		},
	)
}

func (s *Service) DeletePhoto(ctx context.Context, actor, id, photoID int64, requestID string) (int64, error) {
	return s.mutate(ctx, actor, "delete_photo", requestID, []int64{id, photoID}, func(tx bun.Tx) (int64, error) {
		if _, e := pocketTx(ctx, tx, actor, id, 0, false); e != nil {
			return 0, e
		}
		ph := new(Photo)
		if e := tx.NewSelect().Model(ph).Where("id=? AND pocket_id=?", photoID, id).For("UPDATE").Scan(ctx); e != nil {
			return 0, e
		}
		if ph.DeletedAt != nil {
			return 0, nil
		}
		_, e := tx.NewUpdate().
			Model(ph).
			Set("status='deleting',latest_job_id=NULL,updated_at=now()").
			WherePK().
			Exec(ctx)
		if e != nil {
			return 0, e
		}
		if _, e = tx.NewDelete().Model((*Match)(nil)).Where("photo_id=?", photoID).Exec(ctx); e != nil {
			return 0, e
		}
		payload, err := json.Marshal([]int64{photoID})
		if err != nil {
			return 0, err
		}
		return queue(
			ctx,
			tx,
			&Job{
				Kind:      "delete_photos",
				OwnerID:   actor,
				PocketID:  &id,
				Payload:   payload,
				DedupeKey: fmt.Sprintf("photo:delete:%d", photoID),
			},
		)
	})
}

func (s *Service) StartRecognition(
	ctx context.Context,
	actor, id int64,
	photos []int64,
	requestID string,
) (int64, error) {
	if s.Faces == nil || s.Storage == nil {
		return 0, ErrUnavailable
	}
	if e := distinctIDs(photos, 10); e != nil {
		return 0, e
	}
	return s.mutate(ctx, actor, "recognize", requestID, []any{id, photos}, func(tx bun.Tx) (int64, error) {
		p, e := pocketTx(ctx, tx, actor, id, 0, true)
		if e != nil {
			return 0, e
		}
		var found []Photo
		if e = tx.NewSelect().
			Model(&found).
			Where("pocket_id=? AND id IN (?) AND deleted_at IS NULL AND status NOT IN ('deleting','deleted') AND retention_until>now()", id, bun.List(photos)).
			For("UPDATE").
			Scan(ctx); e != nil {
			return 0, e
		}
		if len(found) != len(photos) {
			return 0, ErrDenied
		}
		payload, err := json.Marshal(photos)
		if err != nil {
			return 0, err
		}
		jobID, e := queue(
			ctx,
			tx,
			&Job{
				Kind:         "recognize",
				OwnerID:      actor,
				PocketID:     &id,
				InputVersion: p.Revision,
				Payload:      payload,
				DedupeKey:    fmt.Sprintf("recognize:%d:%d:%s", actor, id, requestID),
			},
		)
		if e != nil {
			return 0, e
		}
		_, e = tx.NewUpdate().
			Model((*Photo)(nil)).
			Set("latest_job_id=?,status='processing',error_code='',updated_at=now()", jobID).
			Where("id IN (?)", bun.List(photos)).
			Exec(ctx)
		return jobID, e
	})
}

func (s *Service) Profile(ctx context.Context, actor int64) (*Profile, int, error) {
	p := new(Profile)
	e := s.DB.NewSelect().Model(p).Where("user_id=?", actor).Scan(ctx)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if e != nil {
		return nil, 0, e
	}
	count, e := s.DB.NewSelect().Model((*Sample)(nil)).Where("profile_id=? AND status='active'", p.ID).Count(ctx)
	return p, count, e
}

type enrollPayload struct {
	UploadIDs []int64 `json:"upload_ids"`
	PersonID  string  `json:"person_id"`
	Consent   string  `json:"consent"`
}

//nolint:gocyclo // Keep the transaction and its version/authorization checks together for auditability.
func (s *Service) Enroll(
	ctx context.Context,
	actor, revision int64,
	uploads []int64,
	consent, requestID string,
	replace bool,
) (int64, error) {
	if s.Faces == nil || s.Storage == nil {
		return 0, ErrUnavailable
	}
	if e := distinctIDs(uploads, 3); e != nil {
		return 0, e
	}
	if consent != s.FacePolicy || s.FacePolicy == "" {
		return 0, failure(connect.CodeInvalidArgument, "请单独同意当前人脸处理告知")
	}
	if replace && revision <= 0 {
		return 0, ErrConflict
	}
	return s.mutate(
		ctx,
		actor,
		"enroll",
		requestID,
		[]any{revision, uploads, consent, replace},
		func(tx bun.Tx) (int64, error) {
			if _, e := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(?)", -actor); e != nil {
				return 0, e
			}
			p := new(Profile)
			e := tx.NewSelect().Model(p).Where("user_id=?", actor).For("UPDATE").Scan(ctx)
			now := time.Now()
			if errors.Is(e, sql.ErrNoRows) {
				if replace {
					return 0, ErrState
				}
				*p = Profile{
					UserID:            actor,
					Status:            "pending",
					Revision:          1,
					EnrollmentVersion: 1,
					ConsentVersion:    consent,
					ConsentedAt:       now,
					ConsentExpiresAt:  now.Add(365 * 24 * time.Hour),
				}
				_, e = tx.NewInsert().Model(p).Returning("*").Exec(ctx)
			} else if e == nil {
				if p.Status == "revoking" {
					return 0, ErrState
				}
				if !replace && p.Status != "revoked" && p.Status != "failed" {
					return 0, ErrState
				}
				if replace && p.Revision != revision {
					return 0, ErrConflict
				}
				p.Revision++
				p.EnrollmentVersion++
				if p.Status != "active" {
					p.Status = "pending"
				}
				p.UpdatedAt = now
				_, e = tx.NewUpdate().Model(p).WherePK().Exec(ctx)
			}
			if e != nil {
				return 0, e
			}
			person := randomUUID()
			for _, uID := range uploads {
				u, e := uploadTx(ctx, tx, actor, uID, "face_sample", 0)
				if e != nil {
					return 0, e
				}
				sample := Sample{
					ProfileID:         p.ID,
					UploadID:          u.ID,
					EnrollmentVersion: p.EnrollmentVersion,
					PersonID:          person,
					Status:            "pending",
				}
				if _, e = tx.NewInsert().Model(&sample).Exec(ctx); e != nil {
					return 0, e
				}
			}
			_, e = tx.ExecContext(
				ctx,
				"INSERT INTO westpocket.consent_event(user_id,purpose,action,policy_version,profile_id) VALUES (?,?,?,?,?)",
				actor,
				"face_enrollment",
				"grant",
				consent,
				p.ID,
			)
			if e != nil {
				return 0, e
			}
			payload, err := json.Marshal(enrollPayload{UploadIDs: uploads, PersonID: person, Consent: consent})
			if err != nil {
				return 0, err
			}
			return queue(
				ctx,
				tx,
				&Job{
					Kind:         "enroll",
					OwnerID:      actor,
					ProfileID:    &p.ID,
					InputVersion: p.EnrollmentVersion,
					Payload:      payload,
					DedupeKey:    fmt.Sprintf("enroll:%d:%d", p.ID, p.EnrollmentVersion),
				},
			)
		},
	)
}

func (s *Service) Revoke(ctx context.Context, actor, revision int64, requestID string) (int64, error) {
	if revision <= 0 {
		return 0, ErrConflict
	}
	return s.mutate(ctx, actor, "revoke", requestID, revision, func(tx bun.Tx) (int64, error) {
		p := new(Profile)
		if e := tx.NewSelect().Model(p).Where("user_id=?", actor).For("UPDATE").Scan(ctx); e != nil {
			return 0, e
		}
		if p.Revision != revision {
			return 0, ErrConflict
		}
		p.Status = "revoking"
		p.Revision++
		p.EnrollmentVersion++
		now := time.Now()
		p.RevokedAt = &now
		p.UpdatedAt = now
		if _, e := tx.NewUpdate().Model(p).WherePK().Exec(ctx); e != nil {
			return 0, e
		}
		if _, e := tx.ExecContext(
			ctx,
			"INSERT INTO westpocket.consent_event(user_id,purpose,action,policy_version,profile_id) VALUES (?,?,?,?,?)",
			actor,
			"face_enrollment",
			"revoke",
			p.ConsentVersion,
			p.ID,
		); e != nil {
			return 0, e
		}
		return queue(
			ctx,
			tx,
			&Job{
				Kind:         "revoke",
				OwnerID:      actor,
				ProfileID:    &p.ID,
				InputVersion: p.EnrollmentVersion,
				DedupeKey:    fmt.Sprintf("revoke:%d:%d", p.ID, p.EnrollmentVersion),
			},
		)
	})
}

func (s *Service) Matches(ctx context.Context, actor, id, jobID int64) ([]Match, error) {
	p, _, e := s.Pocket(ctx, actor, id)
	if e != nil {
		return nil, e
	}
	if p.OwnerID != actor {
		return nil, ErrDenied
	}
	var matches []Match
	q := s.DB.NewSelect().
		Model(&matches).
		Join("JOIN westpocket.pocket_photo ph ON ph.id=fm.photo_id").
		Where("ph.pocket_id=? AND ph.latest_job_id=fm.job_id AND ph.deleted_at IS NULL AND ph.status='ready' AND ph.retention_until>now() AND fm.expires_at>now()", id)
	if jobID > 0 {
		q = q.Where("fm.job_id=?", jobID)
	}
	if e = q.OrderExpr("fm.photo_id,fm.face_index").Scan(ctx); e != nil {
		return nil, e
	}
	var active []Profile
	if e = s.DB.NewSelect().Model(&active).Where("status='active' AND consent_expires_at>now()").Scan(ctx); e != nil {
		return nil, e
	}
	valid := map[int64]bool{}
	for _, p := range active {
		valid[p.UserID] = true
	}
	for i := range matches {
		m := &matches[i]
		filtered := make([]Candidate, 0, len(m.Candidates))
		for _, c := range m.Candidates {
			if valid[c.UserID] {
				filtered = append(filtered, c)
			}
		}
		m.Candidates = filtered
		if m.SuggestedUserID != nil && !valid[*m.SuggestedUserID] {
			m.SuggestedUserID = nil
			m.MatchStatus = "unknown"
			m.Score = 0
		}
	}
	return matches, nil
}

func (s *Service) Resolve(
	ctx context.Context,
	actor, id, matchID, userID, revision int64,
	ignore bool,
	requestID string,
) (int64, error) {
	if revision <= 0 {
		return 0, ErrConflict
	}
	if !ignore {
		users, e := s.Directory.GetUsers(ctx, []int64{userID})
		if e != nil {
			return 0, e
		}
		if len(users) != 1 {
			return 0, failure(connect.CodeInvalidArgument, "用户不存在")
		}
	}
	return s.mutate(
		ctx,
		actor,
		"resolve",
		requestID,
		[]any{id, matchID, userID, revision, ignore},
		func(tx bun.Tx) (int64, error) {
			p, e := pocketTx(ctx, tx, actor, id, revision, true)
			if e != nil {
				return 0, e
			}
			m := new(Match)
			if e = tx.NewSelect().
				Model(m).
				Join("JOIN westpocket.pocket_photo ph ON ph.id=fm.photo_id").
				Where("fm.id=? AND ph.pocket_id=? AND ph.latest_job_id=fm.job_id AND ph.deleted_at IS NULL AND fm.expires_at>now()", matchID, id).
				Scan(ctx); e != nil {
				return 0, e
			}
			m.Resolution = "confirmed"
			m.ConfirmedUserID = &userID
			if ignore {
				m.Resolution = "ignored"
				m.ConfirmedUserID = nil
			}
			if _, e = tx.NewUpdate().Model(m).Column("resolution", "confirmed_user_id").WherePK().Exec(ctx); e != nil {
				return 0, e
			}
			return id, touch(ctx, tx, p)
		},
	)
}
