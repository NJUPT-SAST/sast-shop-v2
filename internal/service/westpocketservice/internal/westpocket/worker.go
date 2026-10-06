package westpocket

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog/log"
	"github.com/skip2/go-qrcode"
	"github.com/uptrace/bun"
)

func (s *Service) Run(ctx context.Context) {
	for i := 0; i < 2; i++ {
		go s.work(ctx)
	}
	go func() {
		timer := time.NewTicker(time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if e := s.Purge(ctx); e != nil {
					log.Warn().Str("code", safeError(e)).Msg("West Pocket purge failed")
				}
				observeError(s.reconcile(ctx))
			}
		}
	}()
}

func (s *Service) work(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.runNextJob(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
				log.Warn().Str("code", safeError(err)).Msg("West Pocket job deferred")
			}
			if err := s.runNextNotification(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
				log.Warn().Str("code", safeError(err)).Msg("West Pocket notification deferred")
			}
		}
	}
}

//nolint:gocyclo // Keep the ordered recovery state machine and its fencing checks together for auditability.
func (s *Service) runNextJob(ctx context.Context) error {
	j := new(Job)
	lease := randomUUID()
	err := s.DB.NewRaw(`UPDATE westpocket.async_job SET status='running',lease_owner=?,lease_expires_at=now()+interval '2 minutes',attempt_count=attempt_count+1,updated_at=now() WHERE id=(SELECT id FROM westpocket.async_job WHERE ((status='queued' AND next_attempt_at<=now()) OR (status='running' AND lease_expires_at<now())) ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING *`, lease).
		Scan(ctx, j)
	if err != nil {
		return err
	}
	// Session advisory locks serialize external work without holding database transactions or row locks.
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { closeResource(conn) }()
	lockKey := fmt.Sprintf("wp-job:%s:%d", j.Kind, j.ID)
	if j.PocketID != nil {
		lockKey = fmt.Sprintf("wp-pocket:%d", *j.PocketID)
	}
	if j.ProfileID != nil {
		lockKey = fmt.Sprintf("wp-profile:%d", *j.ProfileID)
	}
	var locked bool
	if err = conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtextextended(?,1))", lockKey).
		Scan(&locked); err != nil {
		return err
	}
	if !locked {
		_, err = s.DB.NewUpdate().
			Model(j).
			Set("status='queued',next_attempt_at=now()+interval '5 seconds',lease_expires_at=NULL,attempt_count=attempt_count-1").
			Where("id=? AND lease_owner=?", j.ID, lease).
			Exec(ctx)
		return err
	}
	defer func() {
		observeExec(conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(hashtextextended(?,1))", lockKey))
	}()
	jobctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-jobctx.Done():
				return
			case <-ticker.C:
				observeExec(
					s.DB.ExecContext(
						jobctx,
						"UPDATE westpocket.async_job SET lease_expires_at=now()+interval '2 minutes' WHERE id=? AND lease_owner=? AND status='running'",
						j.ID,
						lease,
					),
				)
			}
		}
	}()
	switch j.Kind {
	case "publish":
		err = s.publishJob(jobctx, j)
	case "cancel":
		err = s.cancelJob(jobctx, j)
	case "recognize":
		err = s.recognizeJob(jobctx, j)
	case "enroll":
		err = s.enrollJob(jobctx, j)
	case "revoke":
		err = s.revokeJob(jobctx, j)
	case "delete_photos":
		err = s.deletePhotosJob(jobctx, j)
	case "cleanup_enrollment":
		err = s.cleanupEnrollment(jobctx, j)
	default:
		err = errors.New("UNKNOWN_JOB")
	}

	if e := s.finishJob(ctx, j, lease, err); e != nil {
		return e
	}
	return err
}

func (s *Service) finishJob(ctx context.Context, j *Job, lease string, runErr error) error {
	status := "succeeded"
	if runErr != nil {
		status = "queued"
		privacy := j.Kind == "revoke" || j.Kind == "delete_photos" || j.Kind == "cleanup_enrollment"
		terminal := j.AttemptCount >= 5 || connect.CodeOf(runErr) == connect.CodeFailedPrecondition ||
			connect.CodeOf(runErr) == connect.CodeInvalidArgument
		if !privacy && terminal {
			status = "failed"
		}
	}
	return s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		result, e := tx.ExecContext(
			ctx,
			"UPDATE westpocket.async_job SET status=?,error_code=?,next_attempt_at=now()+ (? * interval '1 second'),lease_expires_at=NULL,updated_at=now() WHERE id=? AND lease_owner=?",
			status,
			safeError(runErr),
			min(600, min(j.AttemptCount, 10)*min(j.AttemptCount, 10)*10),
			j.ID,
			lease,
		)
		if e != nil {
			return e
		}
		count, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if count == 0 {
			return nil
		}
		if status == "failed" && j.Kind == "enroll" && j.ProfileID != nil {
			if _, e = queue(
				ctx,
				tx,
				&Job{
					Kind:         "cleanup_enrollment",
					OwnerID:      j.OwnerID,
					ProfileID:    j.ProfileID,
					InputVersion: j.InputVersion,
					Payload:      j.Payload,
					DedupeKey:    fmt.Sprintf("cleanup:%d:%d", *j.ProfileID, j.InputVersion),
				},
			); e != nil {
				return e
			}
			_, e = tx.ExecContext(
				ctx,
				"UPDATE westpocket.face_profile SET status='failed',updated_at=now() WHERE id=? AND enrollment_version=? AND status='pending'",
				*j.ProfileID,
				j.InputVersion,
			)
			return e
		}
		return nil
	})
}

func (s *Service) internalPocket(ctx context.Context, id int64) (*Pocket, []Member, error) {
	p := new(Pocket)
	if e := s.DB.NewSelect().Model(p).Where("id=?", id).Scan(ctx); e != nil {
		return nil, nil, e
	}
	m, e := membersTx(ctx, s.DB, id)
	return p, m, e
}

//nolint:gocyclo // Keep the ordered recovery state machine and its fencing checks together for auditability.
func (s *Service) publishJob(ctx context.Context, j *Job) error {
	p, m, e := s.internalPocket(ctx, *j.PocketID)
	if e != nil {
		return e
	}
	if p.Status != "publishing" {
		return nil
	}
	for i := range m {
		a := &m[i]
		if a.UserID == p.OwnerID {
			continue
		}
		state, _, e := s.internalPocket(ctx, p.ID)
		if e != nil {
			return e
		}
		if state.Status != "publishing" {
			return nil
		}
		bill, e := s.Payments.Create(ctx, p.ID, a.UserID, p.OwnerID, a.ShareCents)
		if e != nil {
			return e
		}
		if bill.PayerID != a.UserID || bill.PayeeID != p.OwnerID || bill.AmountCents != a.ShareCents ||
			bill.SourceType != "west_pocket" ||
			bill.SourceID != p.ID ||
			bill.Status == "closed" {
			return failure(connect.CodeFailedPrecondition, "账单与冻结分摊不一致")
		}
		a.PaymentBillID = &bill.ID
		a.BillStatus = bill.Status
		if _, e = s.DB.NewUpdate().Model(a).Column("payment_bill_id", "bill_status").WherePK().Exec(ctx); e != nil {
			return e
		}
	}
	return s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		current, e := pocketTx(ctx, tx, p.OwnerID, p.ID, 0, false)
		if e != nil {
			return e
		}
		if current.Status != "publishing" {
			return nil
		}
		current.Status = "collecting"
		now := time.Now()
		current.PublishedAt = &now
		if e = touch(ctx, tx, current); e != nil {
			return e
		}
		for _, a := range m {
			if a.UserID != p.OwnerID {
				for _, kind := range []string{"payment", "payment_qr"} {
					if e = enqueueNotification(ctx, tx, p.ID, a.UserID, kind, 0); e != nil {
						return e
					}
				}
			}
		}
		return enqueueNotification(ctx, tx, p.ID, p.OwnerID, "summary", 0)
	})
}

func enqueueNotification(ctx context.Context, tx bun.Tx, pocket, recipient int64, kind string, sequence int) error {
	n := Notification{
		PocketID:        pocket,
		RecipientUserID: recipient,
		Kind:            kind,
		Sequence:        sequence,
		Status:          "pending",
		MessageUUID:     randomUUID(),
	}
	_, e := tx.NewInsert().Model(&n).On("CONFLICT (pocket_id,recipient_user_id,kind,sequence) DO NOTHING").Exec(ctx)
	return e
}

func (s *Service) cancelJob(ctx context.Context, j *Job) error {
	p, _, e := s.internalPocket(ctx, *j.PocketID)
	if e != nil {
		return e
	}
	if p.Status != "cancelling" {
		return nil
	}
	e = s.Payments.Cancel(ctx, p.ID)
	if e != nil {
		if connect.CodeOf(e) == connect.CodeFailedPrecondition {
			observeExec(
				s.DB.ExecContext(
					ctx,
					"UPDATE westpocket.pocket SET status='collecting',revision=revision+1,updated_at=now() WHERE id=? AND status='cancelling'",
					p.ID,
				),
			)
		}
		return e
	}
	return s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		p, e := pocketTx(ctx, tx, p.OwnerID, p.ID, 0, false)
		if e != nil {
			return e
		}
		if p.Status != "cancelling" {
			return ErrState
		}
		p.Status = "cancelled"
		now := time.Now()
		p.CancelledAt = &now
		if e = touch(ctx, tx, p); e != nil {
			return e
		}
		if _, e = tx.ExecContext(
			ctx,
			"UPDATE westpocket.pocket_member SET bill_status='closed',updated_at=now() WHERE pocket_id=? AND user_id<>?",
			p.ID,
			p.OwnerID,
		); e != nil {
			return e
		}
		_, e = tx.ExecContext(
			ctx,
			"UPDATE westpocket.notification_outbox SET status='cancelled',updated_at=now() WHERE pocket_id=? AND status IN ('pending','retrying','unknown','failed')",
			p.ID,
		)
		return e
	})
}

//nolint:gocyclo // Keep the ordered recovery state machine and its fencing checks together for auditability.
func (s *Service) enrollJob(ctx context.Context, j *Job) error {
	if s.Faces == nil || s.Storage == nil {
		return ErrUnavailable
	}
	var payload enrollPayload
	if e := json.Unmarshal(j.Payload, &payload); e != nil {
		return e
	}
	p := new(Profile)
	if e := s.DB.NewSelect().Model(p).Where("id=?", *j.ProfileID).Scan(ctx); e != nil {
		return e
	}
	if p.Status == "revoking" || p.Status == "revoked" || p.EnrollmentVersion != j.InputVersion {
		if p.PersonID == payload.PersonID {
			return s.finishEnrollmentCleanup(ctx, j, payload)
		}
		return s.Faces.Delete(ctx, payload.PersonID)
	}
	if p.PersonID == payload.PersonID && p.Status == "active" {
		return s.finishEnrollmentCleanup(ctx, j, payload)
	}
	images := make([][]byte, 0, len(payload.UploadIDs))
	for _, id := range payload.UploadIDs {
		u := new(Upload)
		if e := s.DB.NewSelect().Model(u).Where("id=? AND deleted_at IS NULL", id).Scan(ctx); e != nil {
			return e
		}
		data, e := s.Storage.Read(ctx, u.ObjectKey)
		if e != nil {
			return e
		}
		images = append(images, data)
	}
	if e := s.Faces.Enroll(ctx, payload.PersonID, images); e != nil {
		return e
	}
	activated := false
	e := s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		current := new(Profile)
		if e := tx.NewSelect().Model(current).Where("id=?", p.ID).For("UPDATE").Scan(ctx); e != nil {
			return e
		}
		if current.Status == "revoking" || current.Status == "revoked" || current.EnrollmentVersion != j.InputVersion {
			return nil
		}
		current.Status = "active"
		current.PersonID = payload.PersonID
		current.Revision++
		current.ConsentVersion = payload.Consent
		current.ConsentedAt = time.Now()
		current.ConsentExpiresAt = time.Now().Add(365 * 24 * time.Hour)
		current.RevokedAt = nil
		current.ProviderDeletedAt = nil
		current.UpdatedAt = time.Now()
		if _, e := tx.NewUpdate().Model(current).WherePK().Exec(ctx); e != nil {
			return e
		}
		if _, e := tx.ExecContext(
			ctx,
			"UPDATE westpocket.face_sample SET status=CASE WHEN enrollment_version=? THEN 'active' ELSE 'deleting' END WHERE profile_id=?",
			j.InputVersion,
			p.ID,
		); e != nil {
			return e
		}
		activated = true
		return nil
	})
	if e != nil {
		return e
	}
	if !activated {
		return s.Faces.Delete(ctx, payload.PersonID)
	}
	return s.finishEnrollmentCleanup(ctx, j, payload)
}

func (s *Service) finishEnrollmentCleanup(ctx context.Context, j *Job, payload enrollPayload) error {
	var old []Sample
	if e := s.DB.NewSelect().
		Model(&old).
		Where("profile_id=? AND person_id<>? AND enrollment_version<? AND status<>'deleted'", *j.ProfileID, payload.PersonID, j.InputVersion).
		Scan(ctx); e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, sample := range old {
		if !seen[sample.PersonID] {
			if e := s.Faces.Delete(ctx, sample.PersonID); e != nil {
				return e
			}
			seen[sample.PersonID] = true
		}
	}
	if _, e := s.DB.ExecContext(
		ctx,
		"UPDATE westpocket.face_sample SET status='deleted' WHERE profile_id=? AND person_id<>? AND enrollment_version<?",
		*j.ProfileID,
		payload.PersonID,
		j.InputVersion,
	); e != nil {
		return e
	}
	for _, id := range payload.UploadIDs {
		if e := s.deleteUpload(ctx, id); e != nil {
			return e
		}
	}
	return nil
}

func (s *Service) revokeJob(ctx context.Context, j *Job) error {
	if s.Faces == nil || s.Storage == nil {
		return ErrUnavailable
	}
	profile := new(Profile)
	if e := s.DB.NewSelect().Model(profile).Where("id=?", *j.ProfileID).Scan(ctx); e != nil {
		return e
	}
	if profile.Status != "revoking" || profile.EnrollmentVersion != j.InputVersion {
		return nil
	}
	var samples []Sample
	if e := s.DB.NewSelect().
		Model(&samples).
		Where("profile_id=? AND enrollment_version<=?", *j.ProfileID, j.InputVersion).
		Scan(ctx); e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, sample := range samples {
		if !seen[sample.PersonID] {
			if e := s.Faces.Delete(ctx, sample.PersonID); e != nil {
				return e
			}
			seen[sample.PersonID] = true
		}
		if e := s.deleteUpload(ctx, sample.UploadID); e != nil {
			return e
		}
	}
	return s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, e := tx.ExecContext(
			ctx,
			"UPDATE westpocket.face_sample SET status='deleted' WHERE profile_id=?",
			*j.ProfileID,
		); e != nil {
			return e
		}
		if _, e := tx.ExecContext(
			ctx,
			"UPDATE westpocket.face_match SET suggested_user_id=NULL,score=0,match_status='unknown',candidates='[]'::jsonb WHERE suggested_user_id=? OR candidates @> ?::jsonb",
			j.OwnerID,
			fmt.Sprintf(`[{"user_id":%d}]`, j.OwnerID),
		); e != nil {
			return e
		}
		_, e := tx.ExecContext(
			ctx,
			"UPDATE westpocket.face_profile SET status='revoked',person_id='',provider_deleted_at=now(),revision=revision+1,updated_at=now() WHERE id=? AND enrollment_version=? AND status='revoking'",
			*j.ProfileID,
			j.InputVersion,
		)
		return e
	})
}

//nolint:gocyclo // Keep the ordered recovery state machine and its fencing checks together for auditability.
func (s *Service) recognizeJob(ctx context.Context, j *Job) error {
	if s.Faces == nil || s.Storage == nil {
		return ErrUnavailable
	}
	var ids []int64
	if e := json.Unmarshal(j.Payload, &ids); e != nil {
		return e
	}
	completed, failed := 0, 0
	var last error
	for _, id := range ids {
		ph := new(Photo)
		if e := s.DB.NewSelect().Model(ph).Where("id=?", id).Scan(ctx); e != nil {
			return e
		}
		if ph.LatestJobID == nil || *ph.LatestJobID != j.ID || ph.DeletedAt != nil || ph.Status == "deleting" ||
			time.Now().After(ph.RetentionUntil) {
			continue
		}
		if ph.Status == "ready" {
			completed++
			continue
		}
		p, _, e := s.internalPocket(ctx, ph.PocketID)
		if e != nil {
			return e
		}
		if p.Status != "draft" {
			return nil
		}
		data, e := s.Storage.Read(ctx, ph.ObjectKey)
		var faces []Face
		if e == nil {
			faces, e = s.Faces.Recognize(ctx, data)
		}
		if e != nil {
			failed++
			last = e
			observeExec(
				s.DB.ExecContext(
					ctx,
					"UPDATE westpocket.pocket_photo SET status='failed',error_code=?,updated_at=now() WHERE id=? AND latest_job_id=? AND deleted_at IS NULL AND status<>'deleting'",
					safeError(e),
					id,
					j.ID,
				),
			)
			continue
		}
		var profiles []Profile
		if e = s.DB.NewSelect().
			Model(&profiles).
			Where("status='active' AND consent_expires_at>now()").
			Scan(ctx); e != nil {
			return e
		}
		mapping := map[string]int64{}
		for _, p := range profiles {
			mapping[p.PersonID] = p.UserID
		}
		matches := MatchFaces(faces, mapping, s.Threshold, s.Margin)
		e = s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			current := new(Photo)
			if e := tx.NewSelect().Model(current).Where("id=?", id).For("UPDATE").Scan(ctx); e != nil {
				return e
			}
			if current.LatestJobID == nil || *current.LatestJobID != j.ID || current.DeletedAt != nil ||
				current.Status == "deleting" ||
				time.Now().After(current.RetentionUntil) {
				return nil
			}
			if _, e := tx.NewDelete().
				Model((*Match)(nil)).
				Where("photo_id=? AND job_id=?", id, j.ID).
				Exec(ctx); e != nil {
				return e
			}
			for i := range matches {
				m := &matches[i]
				m.PhotoID = id
				m.JobID = j.ID
				m.FaceIndex = i
				m.ExpiresAt = time.Now().Add(24 * time.Hour)
				if _, e := tx.NewInsert().Model(m).Exec(ctx); e != nil {
					return e
				}
			}
			_, e := tx.ExecContext(
				ctx,
				"UPDATE westpocket.pocket_photo SET status='ready',detected_face_count=?,error_code='',updated_at=now() WHERE id=?",
				len(faces),
				id,
			)
			return e
		})
		if e != nil {
			return e
		}
		completed++
	}
	progress, marshalErr := json.Marshal(map[string]int{"total": len(ids), "completed": completed, "failed": failed})
	if marshalErr != nil {
		return marshalErr
	}
	observeExec(
		s.DB.ExecContext(
			ctx,
			"UPDATE westpocket.async_job SET progress=?::jsonb,updated_at=now() WHERE id=? AND lease_owner=?",
			string(progress),
			j.ID,
			j.LeaseOwner,
		),
	)
	return last
}

// MatchFaces keeps uncertain candidates for manual resolution and flags duplicate identities in one photo.
func MatchFaces(faces []Face, mapping map[string]int64, threshold, margin float64) []Match {
	out := make([]Match, len(faces))
	counts := map[int64]int{}
	for i, f := range faces {
		m := Match{
			BboxX:       f.X,
			BboxY:       f.Y,
			BboxWidth:   f.Width,
			BboxHeight:  f.Height,
			MatchStatus: f.Status,
			Resolution:  "pending",
			Candidates:  []Candidate{},
		}
		if m.MatchStatus == "" {
			m.MatchStatus = "unknown"
		}
		for _, c := range f.Candidates {
			if user := mapping[c.PersonID]; user > 0 {
				m.Candidates = append(m.Candidates, Candidate{UserID: user, Score: c.Score})
			}
		}
		sort.Slice(m.Candidates, func(i, j int) bool { return m.Candidates[i].Score > m.Candidates[j].Score })
		if len(m.Candidates) > 0 && m.MatchStatus != "low_quality" {
			m.Score = m.Candidates[0].Score
			if m.Score >= threshold && (len(m.Candidates) == 1 || m.Score-m.Candidates[1].Score >= margin) {
				m.SuggestedUserID = ptr(m.Candidates[0].UserID)
				m.MatchStatus = "matched"
				counts[*m.SuggestedUserID]++
			} else {
				m.MatchStatus = "ambiguous"
			}
		}
		out[i] = m
	}
	for i := range out {
		if out[i].SuggestedUserID != nil && counts[*out[i].SuggestedUserID] > 1 {
			out[i].SuggestedUserID = nil
			out[i].MatchStatus = "ambiguous"
		}
	}
	return out
}

func (s *Service) withUploadLock(ctx context.Context, id int64, fn func(bun.Conn) error) error {
	conn, e := s.DB.Conn(ctx)
	if e != nil {
		return e
	}
	defer func() { closeResource(conn) }()
	key := fmt.Sprintf("wp-upload:%d", id)
	if _, e = conn.ExecContext(ctx, "SELECT pg_advisory_lock(hashtextextended(?,2))", key); e != nil {
		return e
	}
	defer func() {
		observeExec(conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock(hashtextextended(?,2))", key))
	}()
	return fn(conn)
}

func (s *Service) deleteUpload(ctx context.Context, id int64) error {
	return s.withUploadLock(ctx, id, func(conn bun.Conn) error {
		u := new(Upload)
		if e := conn.NewSelect().Model(u).Where("id=?", id).Scan(ctx); e != nil {
			return e
		}
		if u.DeletedAt != nil {
			return nil
		}
		if s.Storage == nil {
			return ErrUnavailable
		}
		if e := s.Storage.Delete(ctx, u.ObjectKey); e != nil {
			return e
		}
		_, e := conn.ExecContext(ctx, "UPDATE westpocket.upload SET deleted_at=now(),status='deleted' WHERE id=?", id)
		return e
	})
}

func (s *Service) deletePhotosJob(ctx context.Context, j *Job) error {
	var ids []int64
	if e := json.Unmarshal(j.Payload, &ids); e != nil {
		return e
	}
	for _, id := range ids {
		ph := new(Photo)
		if e := s.DB.NewSelect().Model(ph).Where("id=?", id).Scan(ctx); e != nil {
			return e
		}
		if e := s.deleteUpload(ctx, ph.UploadID); e != nil {
			return e
		}
		if _, e := s.DB.ExecContext(
			ctx,
			"UPDATE westpocket.pocket_photo SET object_key='',status='deleted',deleted_at=now(),latest_job_id=NULL,updated_at=now() WHERE id=?",
			id,
		); e != nil {
			return e
		}
	}
	return nil
}

func (s *Service) Purge(ctx context.Context) error {
	var expired []Profile
	if e := s.DB.NewSelect().
		Model(&expired).
		Where("status IN ('active','pending','failed') AND consent_expires_at<=now()").
		Limit(100).
		Scan(ctx); e != nil {
		return e
	}
	for _, p := range expired {
		if _, e := s.Revoke(
			ctx,
			p.UserID,
			p.Revision,
			randomUUID(),
		); e != nil &&
			connect.CodeOf(e) != connect.CodeAborted {
			return e
		}
	}
	var photos []Photo
	if e := s.DB.NewSelect().
		Model(&photos).
		Where("retention_until<=now() AND deleted_at IS NULL AND status<>'deleting'").
		Limit(100).
		Scan(ctx); e != nil {
		return e
	}
	for _, ph := range photos {
		if _, e := s.DeletePhoto(ctx, ph.UploaderID, ph.PocketID, ph.ID, randomUUID()); e != nil {
			return e
		}
	}
	if _, e := s.DB.NewDelete().Model((*Match)(nil)).Where("expires_at<=now()").Exec(ctx); e != nil {
		return e
	}
	var uploads []Upload
	if e := s.DB.NewSelect().
		Model(&uploads).
		Where("expires_at<=now() AND deleted_at IS NULL AND (bound=false OR purpose='face_sample')").
		Limit(100).
		Scan(ctx); e != nil {
		return e
	}
	for _, u := range uploads {
		if e := s.deleteUpload(ctx, u.ID); e != nil {
			return e
		}
	}
	return nil
}

func (s *Service) Refresh(ctx context.Context, id int64) error {
	p, m, e := s.internalPocket(ctx, id)
	if e != nil {
		return e
	}
	if !collectionReadable(p.Status) {
		return nil
	}
	var ids []int64
	for _, a := range m {
		if a.PaymentBillID != nil {
			ids = append(ids, *a.PaymentBillID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	bills, e := s.Payments.Bills(ctx, ids)
	if e != nil {
		return e
	}
	if len(bills) != len(ids) {
		return ErrUnavailable
	}
	completed := true
	for _, b := range bills {
		if !billSourceMatches(b, p) {
			return ErrState
		}
		if _, e = s.DB.ExecContext(
			ctx,
			"UPDATE westpocket.pocket_member SET bill_status=?,updated_at=now() WHERE pocket_id=? AND user_id=? AND payment_bill_id=? AND share_cents=?",
			b.Status,
			id,
			b.PayerID,
			b.ID,
			b.AmountCents,
		); e != nil {
			return e
		}
		completed = completed && b.Status == "completed"
	}
	if completed && p.Status == "collecting" {
		_, e = s.DB.ExecContext(
			ctx,
			"UPDATE westpocket.pocket SET status='settled',settled_at=now(),revision=revision+1,updated_at=now() WHERE id=? AND status='collecting'",
			id,
		)
	}
	return e
}

func (s *Service) reconcile(ctx context.Context) error {
	var after int64
	var failures []error
	for {
		var pockets []Pocket
		if e := s.DB.NewSelect().
			Model(&pockets).
			Where("status='collecting' AND id>?", after).
			Order("id ASC").
			Limit(50).
			Scan(ctx); e != nil {
			return errors.Join(append(failures, e)...)
		}
		for _, p := range pockets {
			if e := s.Refresh(ctx, p.ID); e != nil {
				failures = append(failures, e)
			}
			after = p.ID
		}
		if len(pockets) < 50 {
			return errors.Join(failures...)
		}
	}
}

func (s *Service) runNextNotification(ctx context.Context) error {
	n := new(Notification)
	e := s.DB.NewRaw(`UPDATE westpocket.notification_outbox SET status='sending',lease_expires_at=now()+interval '2 minutes',attempt_count=attempt_count+1,updated_at=now() WHERE id=(SELECT id FROM westpocket.notification_outbox WHERE (status IN ('pending','retrying','unknown') AND next_attempt_at<=now()) OR (status='sending' AND lease_expires_at<now()) ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING *`).
		Scan(ctx, n)
	if e != nil {
		return e
	}
	msgID, e := s.sendNotification(ctx, n)
	status := "sent"
	if e != nil {
		status = "retrying"
		if n.AttemptCount >= 5 {
			status = "failed"
		}
	}
	if errors.Is(e, ErrState) {
		status = "cancelled"
	}
	_, updateErr := s.DB.ExecContext(
		ctx,
		"UPDATE westpocket.notification_outbox SET status=?,feishu_message_id=?,error_code=?,next_attempt_at=now()+(? * interval '1 second'),lease_expires_at=NULL,updated_at=now() WHERE id=? AND attempt_count=?",
		status,
		msgID,
		safeError(e),
		min(600, n.AttemptCount*n.AttemptCount*10),
		n.ID,
		n.AttemptCount,
	)
	if updateErr != nil {
		return updateErr
	}
	return e
}

func (s *Service) sendNotification(ctx context.Context, n *Notification) (string, error) {
	if s.Messenger == nil {
		return "", ErrUnavailable
	}
	p, m, e := s.internalPocket(ctx, n.PocketID)
	if e != nil {
		return "", e
	}
	if !collectionReadable(p.Status) {
		return "", ErrState
	}
	openID, e := s.Directory.OpenID(ctx, n.RecipientUserID)
	if e != nil {
		return "", e
	}
	link := strings.TrimRight(s.MobileURL, "/") + fmt.Sprintf("/west-pocket/%d", p.ID)
	var text string
	var qr []byte
	if n.Kind == "summary" {
		text, e = s.summaryText(ctx, p, m)
	} else {
		text, qr, e = s.paymentText(ctx, p, n, m)
		link += "/pay"
	}
	if e != nil {
		return "", e
	}
	return s.Messenger.Send(ctx, openID, n.MessageUUID, text, link, qr)
}

func (s *Service) summaryText(ctx context.Context, p *Pocket, m []Member) (string, error) {
	text := fmt.Sprintf(
		"West Pocket · %s\n总金额：¥%.2f\n参与人数：%d\n自身份额：¥%.2f\n应收合计：¥%.2f",
		p.Title,
		float64(p.TotalCents)/100,
		p.ParticipantCount,
		float64(p.OwnerShareCents)/100,
		float64(p.ReceivableCents)/100,
	)
	ids := make([]int64, 0, len(m))
	for _, a := range m {
		ids = append(ids, a.UserID)
	}
	users, e := s.Directory.GetUsers(ctx, ids)
	if e != nil {
		return "", e
	}
	names := map[int64]string{}
	for _, u := range users {
		names[u.ID] = u.Name
	}
	for _, a := range m {
		text += fmt.Sprintf("\n%s：¥%.2f", names[a.UserID], float64(a.ShareCents)/100)
	}
	return text, nil
}

func (s *Service) paymentText(ctx context.Context, p *Pocket, n *Notification, m []Member) (string, []byte, error) {
	var member *Member
	for i := range m {
		if m[i].UserID == n.RecipientUserID {
			member = &m[i]
			break
		}
	}
	if member == nil || member.PaymentBillID == nil {
		return "", nil, ErrState
	}
	bills, e := s.Payments.Bills(ctx, []int64{*member.PaymentBillID})
	if e != nil {
		return "", nil, e
	}
	if len(bills) != 1 {
		return "", nil, ErrUnavailable
	}
	bill := bills[0]
	if bill.Status != "unpaid" {
		return "", nil, ErrState
	}
	organizer, e := s.displayName(ctx, p.OwnerID)
	if e != nil {
		return "", nil, e
	}
	text := fmt.Sprintf(
		"West Pocket · %s\n发起人：%s\n待付金额：¥%.2f\n账单：%s\n核验码：%s\n请核对收款人，使用微信扫码付款后在账单中标记已付款，等待垫付人确认。",
		p.Title,
		organizer,
		float64(member.ShareCents)/100,
		bill.BillNo,
		bill.VerifyCode,
	)
	var qr []byte
	if n.Kind == "payment_qr" {
		qr, e = s.qrPNG(p)
	}
	return text, qr, e
}

func (s *Service) displayName(ctx context.Context, id int64) (string, error) {
	users, e := s.Directory.GetUsers(ctx, []int64{id})
	if e != nil {
		return "", e
	}
	if len(users) != 1 {
		return "", ErrUnavailable
	}
	return users[0].Name, nil
}

func (s *Service) qrPNG(p *Pocket) ([]byte, error) {
	plain, e := s.Vault.Decrypt(p.QrCiphertext, p.ID)
	if e != nil {
		return nil, e
	}
	var snap QR
	if e = json.Unmarshal(plain, &snap); e != nil {
		return nil, e
	}
	return qrcode.Encode(snap.Content, qrcode.Medium, 512)
}
func collectionReadable(status string) bool { return status == "collecting" || status == "settled" }

func (s *Service) cleanupEnrollment(ctx context.Context, j *Job) error {
	if s.Faces == nil {
		return ErrUnavailable
	}
	var payload enrollPayload
	if e := json.Unmarshal(j.Payload, &payload); e != nil {
		return e
	}
	p := new(Profile)
	if e := s.DB.NewSelect().Model(p).Where("id=?", *j.ProfileID).Scan(ctx); e != nil {
		return e
	}
	if p.PersonID == payload.PersonID {
		return s.finishEnrollmentCleanup(ctx, j, payload)
	}
	if e := s.Faces.Delete(ctx, payload.PersonID); e != nil {
		return e
	}
	if _, e := s.DB.ExecContext(
		ctx,
		"UPDATE westpocket.face_sample SET status='rejected' WHERE profile_id=? AND person_id=?",
		p.ID,
		payload.PersonID,
	); e != nil {
		return e
	}
	return nil
}

func billSourceMatches(b Bill, p *Pocket) bool {
	return b.SourceType == "west_pocket" && b.SourceID == p.ID && b.PayeeID == p.OwnerID
}
