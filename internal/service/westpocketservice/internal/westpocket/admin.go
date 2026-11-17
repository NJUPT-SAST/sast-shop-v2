package westpocket

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type adminPocket struct {
	ID               int64      `bun:"id"`
	OwnerID          int64      `bun:"owner_id"`
	Title            string     `bun:"title"`
	TotalCents       int32      `bun:"total_cents"`
	Status           string     `bun:"status"`
	Revision         int64      `bun:"revision"`
	LifecycleRound   int64      `bun:"lifecycle_round"`
	AdminOperationID string     `bun:"admin_operation_id"`
	ParticipantCount int32      `bun:"participant_count"`
	OwnerShareCents  int32      `bun:"owner_share_cents"`
	ReceivableCents  int32      `bun:"receivable_cents"`
	PublishedAt      *time.Time `bun:"published_at"`
	CreatedAt        time.Time  `bun:"created_at"`
	UpdatedAt        time.Time  `bun:"updated_at"`
}

const adminPocketColumns = "id,owner_id,title,total_cents,status,revision,lifecycle_round,admin_operation_id,participant_count,owner_share_cents,receivable_cents,published_at,created_at,updated_at"

type adminPocketMember struct {
	ID            int64  `bun:"id"`
	UserID        int64  `bun:"user_id"`
	ShareCents    int32  `bun:"share_cents"`
	PaymentBillID *int64 `bun:"payment_bill_id"`
	BillStatus    string `bun:"bill_status"`
}
type adminPocketSnapshot struct {
	Pocket           adminPocket
	Members          []adminPocketMember
	Bills            []*av.SourceBill
	RestorationBills []*av.SourceBill
	TargetStatus     string
}

func pocketAdminRecord(p *adminPocket) *av.AdminRecord {
	amount := int64(p.TotalCents)
	var actions []string
	if p.AdminOperationID == "" {
		if p.Status == "cancelled" {
			actions = append(actions, "reopen")
		} else {
			actions = append(actions, "cancel")
		}
		if p.Status == "settled" {
			actions = append(actions, "reverse_completion")
		}
		if p.Status == "collecting" || p.Status == "settled" {
			actions = append(actions, "retry_notifications")
		}
	}
	return &av.AdminRecord{
		Resource:       av.ResourceKind_RESOURCE_KIND_POCKET,
		Id:             fmt.Sprint(p.ID),
		Title:          p.Title,
		Status:         p.Status,
		OwnerId:        fmt.Sprint(p.OwnerID),
		CreatedAt:      timestamppb.New(p.CreatedAt),
		UpdatedAt:      timestamppb.New(p.UpdatedAt),
		LifecycleRound: p.LifecycleRound,
		AmountCents:    &amount,
		AllowedActions: actions,
		Fields: []*av.RecordField{
			{Key: "participant_count", Label: "参与人数", Value: fmt.Sprint(p.ParticipantCount)},
			{Key: "owner_share_cents", Label: "主办人份额（分）", Value: fmt.Sprint(p.OwnerShareCents)},
			{Key: "receivable_cents", Label: "应收金额（分）", Value: fmt.Sprint(p.ReceivableCents)},
			{Key: "revision", Label: "内容版本", Value: fmt.Sprint(p.Revision)},
			{Key: "admin_operation_id", Label: "处理中操作", Value: p.AdminOperationID},
		},
	}
}

func (s *Service) adminReadPocket(ctx context.Context, db bun.IDB, id int64, lock bool) (*adminPocket, error) {
	p := new(adminPocket)
	q := db.NewSelect().TableExpr("westpocket.pocket").ColumnExpr(adminPocketColumns).Where("id = ?", id)
	if lock {
		q = q.For("UPDATE")
	}
	if e := q.Scan(ctx, p); e != nil {
		return nil, rpcError(e)
	}
	return p, nil
}

//nolint:gocyclo // Validate the two supported projections and their optional filters.
func (s *Service) AdminList(ctx context.Context, r *av.ListRecordsRequest) (*av.ListRecordsResponse, error) {
	if _, e := admin.Require(ctx); e != nil {
		return nil, e
	}
	page, size, e := admin.Page(r.Page, r.PageSize)
	if e != nil {
		return nil, e
	}
	if e = admin.TimeRange(r.CreatedFrom, r.CreatedTo); e != nil {
		return nil, e
	}
	if !utf8.ValidString(r.Keyword) || strings.ContainsRune(r.Keyword, '\x00') ||
		utf8.RuneCountInString(r.Keyword) > 100 {
		return nil, failure(connect.CodeInvalidArgument, "搜索关键词无效")
	}
	out := &av.ListRecordsResponse{Page: page, PageSize: size}
	if r.Resource == av.ResourceKind_RESOURCE_KIND_JOB {
		return s.adminListJobs(ctx, r, out)
	}
	if r.Resource != av.ResourceKind_RESOURCE_KIND_POCKET {
		return nil, failure(connect.CodeInvalidArgument, "仅支持活动与运营任务")
	}
	var rows []adminPocket
	q := s.DB.NewSelect().TableExpr("westpocket.pocket").ColumnExpr(adminPocketColumns)
	if r.Keyword != "" {
		q = q.Where("strpos(lower(title),lower(?))>0 OR id::text=?", r.Keyword, r.Keyword)
	}
	if r.Status != "" {
		q = q.Where("status=?", r.Status)
	}
	if r.UserId != "" {
		id, e := admin.ParseID(r.UserId)
		if e != nil {
			return nil, e
		}
		q = q.Where("owner_id=?", id)
	}
	if r.CreatedFrom != nil {
		q = q.Where("created_at>=?", r.CreatedFrom.AsTime())
	}
	if r.CreatedTo != nil {
		q = q.Where("created_at<=?", r.CreatedTo.AsTime())
	}
	count, e := q.OrderExpr("created_at DESC,id DESC").
		Limit(int(size)).
		Offset(int((page-1)*size)).
		ScanAndCount(ctx, &rows)
	if e != nil {
		return nil, e
	}
	out.TotalCount = int64(count)
	for i := range rows {
		out.Records = append(out.Records, pocketAdminRecord(&rows[i]))
	}
	return out, nil
}

type adminJob struct {
	ID                   int64
	Kind                 string
	OwnerID              int64
	PocketID             int64
	Status               string
	AttemptCount         int
	ErrorCode            string
	LifecycleRound       int64
	CreatedAt, UpdatedAt time.Time
}

const adminJobColumns = "j.id,j.kind,j.owner_id,j.pocket_id,j.status,j.attempt_count,j.error_code,j.lifecycle_round,j.created_at,j.updated_at"

func jobAdminRecord(j *adminJob) *av.AdminRecord {
	var actions []string
	if j.Status == "failed" || j.Status == "partial_failed" {
		actions = []string{"retry"}
	}
	return &av.AdminRecord{
		Resource:       av.ResourceKind_RESOURCE_KIND_JOB,
		Id:             fmt.Sprint(j.ID),
		Title:          "活动运营任务 · " + j.Kind,
		Status:         j.Status,
		OwnerId:        fmt.Sprint(j.OwnerID),
		LifecycleRound: j.LifecycleRound,
		CreatedAt:      timestamppb.New(j.CreatedAt),
		UpdatedAt:      timestamppb.New(j.UpdatedAt),
		AllowedActions: actions,
		Fields: []*av.RecordField{
			{Key: "pocket_id", Label: "活动编号", Value: fmt.Sprint(j.PocketID)},
			{Key: "kind", Label: "类型", Value: j.Kind},
			{Key: "attempt_count", Label: "尝试次数", Value: fmt.Sprint(j.AttemptCount)},
			{Key: "error_code", Label: "错误代码", Value: j.ErrorCode},
		},
	}
}

func (s *Service) adminListJobs(
	ctx context.Context,
	r *av.ListRecordsRequest,
	out *av.ListRecordsResponse,
) (*av.ListRecordsResponse, error) {
	var jobs []adminJob
	q := s.DB.NewSelect().
		TableExpr("westpocket.async_job AS j").
		ColumnExpr(adminJobColumns).
		Where("j.kind IN ('publish','cancel') AND j.pocket_id IS NOT NULL")
	if r.Status != "" {
		q = q.Where("j.status=?", r.Status)
	}
	if r.Keyword != "" {
		q = q.Where("j.id::text=? OR j.pocket_id::text=?", r.Keyword, r.Keyword)
	}
	if r.UserId != "" {
		id, e := admin.ParseID(r.UserId)
		if e != nil {
			return nil, e
		}
		q = q.Where("j.owner_id=?", id)
	}
	if r.CreatedFrom != nil {
		q = q.Where("j.created_at>=?", r.CreatedFrom.AsTime())
	}
	if r.CreatedTo != nil {
		q = q.Where("j.created_at<=?", r.CreatedTo.AsTime())
	}
	count, e := q.OrderExpr("j.created_at DESC,j.id DESC").
		Limit(int(out.PageSize)).
		Offset(int((out.Page-1)*out.PageSize)).
		ScanAndCount(ctx, &jobs)
	if e != nil {
		return nil, e
	}
	out.TotalCount = int64(count)
	for i := range jobs {
		out.Records = append(out.Records, jobAdminRecord(&jobs[i]))
	}
	return out, nil
}

func (s *Service) AdminGet(ctx context.Context, r *av.GetRecordRequest) (*av.GetRecordResponse, error) {
	if _, e := admin.Require(ctx); e != nil {
		return nil, e
	}
	id, e := admin.ParseID(r.Id)
	if e != nil {
		return nil, e
	}
	if r.Resource == av.ResourceKind_RESOURCE_KIND_JOB {
		var j adminJob
		e = s.DB.NewSelect().
			TableExpr("westpocket.async_job AS j").
			ColumnExpr(adminJobColumns).
			Where("j.id=? AND j.kind IN ('publish','cancel') AND j.pocket_id IS NOT NULL", id).
			Scan(ctx, &j)
		if e != nil {
			return nil, rpcError(e)
		}
		return &av.GetRecordResponse{Record: jobAdminRecord(&j)}, nil
	}
	if r.Resource != av.ResourceKind_RESOURCE_KIND_POCKET {
		return nil, failure(connect.CodeInvalidArgument, "仅支持活动与运营任务")
	}
	p, e := s.adminReadPocket(ctx, s.DB, id, false)
	if e != nil {
		return nil, e
	}
	result := &av.GetRecordResponse{Record: pocketAdminRecord(p)}
	var members []adminPocketMember
	if e = s.DB.NewSelect().
		TableExpr("westpocket.pocket_member").
		Column("id", "user_id", "share_cents", "payment_bill_id", "bill_status").
		Where("pocket_id=?", id).
		Order("id").
		Scan(ctx, &members); e != nil {
		return nil, e
	}
	for _, m := range members {
		result.Record.Fields = append(
			result.Record.Fields,
			&av.RecordField{
				Key:   fmt.Sprintf("member_%d", m.UserID),
				Label: fmt.Sprintf("成员 %d 份额/账单", m.UserID),
				Value: fmt.Sprintf("%d 分 · %s", m.ShareCents, m.BillStatus),
			},
		)
	}
	return result, nil
}

//nolint:gocyclo // Authorization, deduplication, retry eligibility and audit share one transaction.
func (s *Service) AdminMutate(ctx context.Context, r *av.MutateRecordRequest) (*av.MutateRecordResponse, error) {
	a, e := admin.Require(ctx)
	if e != nil {
		return nil, e
	}
	id, e := admin.ParseID(r.Id)
	if e != nil {
		return nil, e
	}
	if len(r.Values) > 0 || r.ExpectedRound < 1 || r.ExpectedUpdatedAt == nil || !r.ExpectedUpdatedAt.IsValid() {
		return nil, failure(connect.CodeInvalidArgument, "请刷新记录后重试")
	}
	if e = admin.ValidateReason(r.Reason, r.Evidence); e != nil {
		return nil, e
	}
	if (r.Resource != av.ResourceKind_RESOURCE_KIND_JOB || r.Action != "retry") &&
		(r.Resource != av.ResourceKind_RESOURCE_KIND_POCKET || r.Action != "retry_notifications") {
		return nil, failure(connect.CodeInvalidArgument, "此操作必须通过整单纠错预览执行")
	}
	var record *av.AdminRecord
	mutation := admin.Mutation{
		Domain:    "westpocket",
		ActorID:   a.UserID,
		Action:    r.Action,
		RequestID: r.RequestId,
		Input:     r,
	}
	e = s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, e := admin.RequireCurrent(ctx, tx, false); e != nil {
			return e
		}
		dedup, e := admin.BeginMutation(ctx, tx, mutation)
		if e != nil {
			return e
		}
		if dedup.Replay {
			return json.Unmarshal(dedup.Response, &record)
		}
		if r.Resource == av.ResourceKind_RESOURCE_KIND_JOB {
			var j adminJob
			if e = tx.NewSelect().
				TableExpr("westpocket.async_job AS j").
				ColumnExpr(adminJobColumns).
				Where("j.id=? AND j.kind IN ('publish','cancel') AND j.pocket_id IS NOT NULL", id).
				Scan(ctx, &j); e != nil {
				return rpcError(e)
			}
			p, e := s.adminReadPocket(ctx, tx, j.PocketID, true)
			if e != nil {
				return e
			}
			if e = tx.NewSelect().
				TableExpr("westpocket.async_job AS j").
				ColumnExpr(adminJobColumns).
				Where("j.id=?", id).
				For("UPDATE").
				Scan(ctx, &j); e != nil {
				return rpcError(e)
			}
			if p.AdminOperationID != "" || j.LifecycleRound != p.LifecycleRound ||
				j.LifecycleRound != r.ExpectedRound ||
				!j.UpdatedAt.Equal(r.ExpectedUpdatedAt.AsTime()) {
				return ErrConflict
			}
			if j.Status != "failed" && j.Status != "partial_failed" {
				return ErrState
			}
			if j.Kind == "publish" && p.Status != "publishing" || j.Kind == "cancel" && p.Status != "cancelling" {
				return ErrState
			}
			before := jobAdminRecord(&j)
			j.Status = "queued"
			j.AttemptCount = 0
			j.ErrorCode = ""
			j.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
			if _, e = tx.ExecContext(
				ctx,
				`UPDATE westpocket.async_job SET status='queued',attempt_count=0,error_code='',next_attempt_at=now(),lease_expires_at=NULL,updated_at=? WHERE id=?`,
				j.UpdatedAt,
				id,
			); e != nil {
				return e
			}
			record = jobAdminRecord(&j)
			if e = admin.WriteAudit(
				ctx,
				tx,
				admin.Entry{
					Domain:     "westpocket",
					ActorID:    a.UserID,
					Action:     r.Action,
					TargetType: "job",
					TargetID:   id,
					RequestID:  r.RequestId,
					Reason:     r.Reason,
					Evidence:   r.Evidence,
					Before:     map[string]any{"status": before.Status},
					After:      map[string]any{"status": "queued"},
				},
			); e != nil {
				return e
			}
		} else {
			p, e := s.adminReadPocket(ctx, tx, id, true)
			if e != nil {
				return e
			}
			if p.AdminOperationID != "" || p.LifecycleRound != r.ExpectedRound ||
				!p.UpdatedAt.Equal(r.ExpectedUpdatedAt.AsTime()) {
				return ErrConflict
			}
			if !collectionReadable(p.Status) {
				return ErrState
			}
			result, e := tx.ExecContext(
				ctx,
				`UPDATE westpocket.notification_outbox SET status='pending',attempt_count=0,next_attempt_at=now(),updated_at=now() WHERE pocket_id=? AND lifecycle_round=? AND status='failed'`,
				id,
				p.LifecycleRound,
			)
			if e != nil {
				return e
			}
			count, e := result.RowsAffected()
			if e != nil {
				return e
			}
			if count == 0 {
				return failure(connect.CodeFailedPrecondition, "没有可重试的通知")
			}
			if e = admin.WriteAudit(
				ctx,
				tx,
				admin.Entry{
					Domain:     "westpocket",
					ActorID:    a.UserID,
					Action:     r.Action,
					TargetType: "pocket",
					TargetID:   id,
					RequestID:  r.RequestId,
					Reason:     r.Reason,
					Evidence:   r.Evidence,
					Before:     map[string]any{"failed_notifications": count},
					After:      map[string]any{"queued_notifications": count},
				},
			); e != nil {
				return e
			}
			record = pocketAdminRecord(p)
		}
		return admin.CompleteMutation(ctx, tx, mutation, record)
	})
	if e != nil {
		return nil, e
	}
	return &av.MutateRecordResponse{Record: record}, nil
}

func (s *Service) AdminStats(ctx context.Context) (*av.GetStatsResponse, error) {
	if _, e := admin.Require(ctx); e != nil {
		return nil, e
	}
	var stats struct{ Pockets, Collecting, FailedJobs, FailedNotifications int64 }
	e := s.DB.NewRaw(`SELECT (SELECT count(*) FROM westpocket.pocket) AS pockets,(SELECT count(*) FROM westpocket.pocket WHERE status='collecting') AS collecting,(SELECT count(*) FROM westpocket.async_job WHERE kind IN ('publish','cancel') AND status IN ('failed','partial_failed')) AS failed_jobs,(SELECT count(*) FROM westpocket.notification_outbox WHERE status='failed') AS failed_notifications`).
		Scan(ctx, &stats)
	if e != nil {
		return nil, e
	}
	return &av.GetStatsResponse{
		Metrics: []*av.Metric{
			{Key: "pockets", Label: "活动总数", Value: stats.Pockets},
			{Key: "collecting_pockets", Label: "收款中活动", Value: stats.Collecting},
			{Key: "failed_pocket_jobs", Label: "异常运营任务", Value: stats.FailedJobs},
			{Key: "failed_notifications", Label: "失败通知", Value: stats.FailedNotifications},
		},
	}, nil
}
