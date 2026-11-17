package adminflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Plan struct {
	Request           *av.PreviewCorrectionRequest `json:"request"`
	Record            *av.AdminRecord              `json:"record"`
	Snapshot          json.RawMessage              `json:"snapshot"`
	Impacts           []*av.Impact                 `json:"impacts"`
	Warnings          []string                     `json:"warnings"`
	RestorationFields []*av.RecordField            `json:"restoration_fields"`
	Steps             []Step                       `json:"steps"`
}

type Step struct {
	Key         string          `json:"key"`
	Description string          `json:"description"`
	Status      string          `json:"status"`
	Error       string          `json:"error,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}

type Case struct {
	ID            string          `bun:"id"`
	ActorID       int64           `bun:"actor_id"`
	ExecutorID    int64           `bun:"executor_id"`
	Resource      int32           `bun:"resource"`
	ResourceID    int64           `bun:"resource_id"`
	Action        string          `bun:"action"`
	Status        string          `bun:"status"`
	Digest        string          `bun:"digest"`
	PlanJSON      json.RawMessage `bun:"plan"`
	Steps         []Step          `bun:"steps,type:jsonb"`
	RequestID     string          `bun:"request_id"`
	Reserved      bool            `bun:"reserved"`
	LeaseToken    string          `bun:"lease_token"`
	LeaseUntil    *time.Time      `bun:"lease_until"`
	NextAttemptAt time.Time       `bun:"next_attempt_at"`
	Attempts      int             `bun:"attempts"`
	Error         string          `bun:"error"`
	ExpiresAt     time.Time       `bun:"expires_at"`
	CreatedAt     time.Time       `bun:"created_at"`
	UpdatedAt     time.Time       `bun:"updated_at"`
	Plan          Plan            `bun:"-"`
}

type Engine struct {
	DB              *bun.DB
	Domain          string
	Build           func(context.Context, *av.PreviewCorrectionRequest) (*Plan, error)
	Reserve         func(context.Context, bun.Tx, *Case) error
	RollbackReserve func(context.Context, bun.Tx, *Case) error
	Apply           func(context.Context, *Execution) error
}

func (e *Engine) table() bun.Ident { return bun.Ident(e.Domain + ".admin_correction") }

func Failure(code connect.Code, message string) error {
	return connect.NewError(code, errors.New(message))
}

func (e *Engine) Preview(ctx context.Context, req *av.PreviewCorrectionRequest) (*av.PreviewCorrectionResponse, error) {
	actor, err := admin.RequireCurrent(ctx, e.DB, false)
	if err != nil {
		return nil, err
	}
	id, err := validatePreviewRequest(req)
	if err != nil {
		return nil, err
	}
	plan, err := e.Build(ctx, req)
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.Record == nil {
		return nil, Failure(connect.CodeInternal, "无法生成纠错预览")
	}
	plan.Request = req
	data, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	caseID := rand.Text()
	expires := time.Now().Add(15 * time.Minute)
	steps, err := json.Marshal(plan.Steps)
	if err != nil {
		return nil, err
	}
	_, err = e.DB.ExecContext(
		ctx,
		`INSERT INTO ? (id,actor_id,executor_id,resource,resource_id,action,status,digest,plan,steps,expires_at)
 VALUES (?,?,?,?,?,?,'preview',?,?::jsonb,?::jsonb,?)`,
		e.table(),
		caseID,
		actor.UserID,
		actor.UserID,
		int32(req.Resource),
		id,
		req.Action,
		digest,
		string(data),
		string(steps),
		expires,
	)
	if err != nil {
		return nil, err
	}
	return &av.PreviewCorrectionResponse{
		PreviewId:         caseID,
		Digest:            digest,
		ExpiresAt:         timestamppb.New(expires),
		Record:            plan.Record,
		Impacts:           plan.Impacts,
		Warnings:          plan.Warnings,
		RestorationFields: plan.RestorationFields,
	}, nil
}

func validatePreviewRequest(req *av.PreviewCorrectionRequest) (int64, error) {
	var err error
	if req == nil || req.ExpectedUpdatedAt == nil || !req.ExpectedUpdatedAt.IsValid() || req.ExpectedRound < 1 {
		return 0, Failure(connect.CodeInvalidArgument, "请刷新记录后重试")
	}
	if err = admin.ValidateReason(req.Reason, req.Evidence); err != nil {
		return 0, err
	}
	if strings.TrimSpace(req.Evidence) == "" {
		return 0, Failure(connect.CodeInvalidArgument, "整单纠错必须登记核实依据")
	}
	id, err := admin.ParseID(req.Id)
	if err != nil {
		return 0, err
	}
	if len(req.Values) > 30 {
		return 0, Failure(connect.CodeInvalidArgument, "恢复字段过多")
	}
	for k, v := range req.Values {
		if len(k) > 100 || len(v) > 16000 {
			return 0, Failure(connect.CodeInvalidArgument, "恢复字段长度超限")
		}
	}
	return id, nil
}

func (e *Engine) load(ctx context.Context, id string) (*Case, error) {
	if len(id) < 10 || len(id) > 128 {
		return nil, Failure(connect.CodeInvalidArgument, "纠错编号无效")
	}
	c := new(Case)
	err := e.DB.NewSelect().TableExpr("?", e.table()).Column("*").Where("id = ?", id).Scan(ctx, c)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Failure(connect.CodeNotFound, "纠错记录不存在")
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(c.PlanJSON, &c.Plan); err != nil {
		return nil, err
	}
	return c, nil
}

func (e *Engine) Get(ctx context.Context, req *av.GetCorrectionRequest) (*av.GetCorrectionResponse, error) {
	if _, err := admin.Require(ctx); err != nil {
		return nil, err
	}
	c, err := e.load(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &av.GetCorrectionResponse{Correction: c.Proto()}, nil
}

func (e *Engine) List(ctx context.Context, req *av.ListCorrectionsRequest) (*av.ListCorrectionsResponse, error) {
	if _, err := admin.Require(ctx); err != nil {
		return nil, err
	}
	page, size, err := admin.Page(req.GetPage(), req.GetPageSize())
	if err != nil {
		return nil, err
	}
	query := e.DB.NewSelect().TableExpr("?", e.table()).Column("*")
	if req.Status != "" {
		query = query.Where("status = ?", req.Status)
	}
	if req.ResourceId != "" {
		id, xerr := admin.ParseID(req.ResourceId)
		if xerr != nil {
			return nil, xerr
		}
		query = query.Where("resource_id = ?", id)
	}
	var cases []Case
	count, err := query.OrderExpr("created_at DESC,id DESC").
		Limit(int(size)).
		Offset(int((page-1)*size)).
		ScanAndCount(ctx, &cases)
	if err != nil {
		return nil, err
	}
	out := &av.ListCorrectionsResponse{TotalCount: int64(count)}
	for i := range cases {
		if err = json.Unmarshal(cases[i].PlanJSON, &cases[i].Plan); err != nil {
			return nil, err
		}
		out.Corrections = append(out.Corrections, cases[i].Proto())
	}
	return out, nil
}

func (c *Case) Proto() *av.Correction {
	result := &av.Correction{
		Id:         c.ID,
		Resource:   av.ResourceKind(c.Resource),
		ResourceId: fmt.Sprint(c.ResourceID),
		Action:     c.Action,
		Status:     c.Status,
		ActorId: fmt.Sprint(
			c.ActorID,
		),
		CreatedAt: timestamppb.New(c.CreatedAt),
		UpdatedAt: timestamppb.New(c.UpdatedAt),
		Error:     c.Error,
		Digest:    c.Digest,
		RequestId: c.RequestID,
	}
	if c.Plan.Request != nil {
		result.Reason = c.Plan.Request.Reason
		result.Evidence = c.Plan.Request.Evidence
		result.LifecycleRound = c.Plan.Request.ExpectedRound
	}
	result.Impacts = c.Plan.Impacts
	for _, s := range c.Steps {
		result.Steps = append(
			result.Steps,
			&av.CorrectionStep{Key: s.Key, Description: s.Description, Status: s.Status, Error: s.Error},
		)
	}
	return result
}

func (e *Engine) Execute(ctx context.Context, req *av.ExecuteCorrectionRequest) (*av.ExecuteCorrectionResponse, error) {
	actor, err := admin.RequireCurrent(ctx, e.DB, false)
	if err != nil {
		return nil, err
	}
	if req == nil || len(req.RequestId) < 8 || len(req.RequestId) > 128 {
		return nil, Failure(connect.CodeInvalidArgument, "缺少有效请求编号")
	}
	c, err := e.load(ctx, req.PreviewId)
	if err != nil {
		return nil, err
	}
	if err = validateExecution(c, actor.UserID, req); err != nil {
		return nil, err
	}
	if c.Status == "completed" {
		return &av.ExecuteCorrectionResponse{Correction: c.Proto()}, nil
	}
	lease := rand.Text()
	var changed []Case
	err = e.DB.NewRaw(`UPDATE ? SET status='executing',request_id=?,executor_id=?,lease_token=?,lease_until=now()+interval '90 seconds',attempts=attempts+1,updated_at=now()
 WHERE id=? AND (lease_until IS NULL OR lease_until<now()) AND (request_id='' OR request_id=?) AND status NOT IN ('completed','failed') RETURNING *`,
		e.table(), req.RequestId, actor.UserID, lease, c.ID, req.RequestId).
		Scan(ctx, &changed)
	if err != nil {
		return nil, err
	}
	if len(changed) == 0 {
		c, err = e.load(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		return &av.ExecuteCorrectionResponse{Correction: c.Proto()}, nil
	}
	c = &changed[0]
	if err = json.Unmarshal(c.PlanJSON, &c.Plan); err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err = e.reserve(ctx, runCtx, c); err != nil {
		return nil, err
	}
	execution := &Execution{Engine: e, Case: c, Lease: lease}
	err = e.Apply(runCtx, execution)
	status, message := e.executionOutcome(ctx, execution, err)
	_, saveErr := e.DB.ExecContext(
		context.WithoutCancel(ctx),
		`UPDATE ? SET status=?,error=?,lease_until=NULL,next_attempt_at=now()+interval '30 seconds',updated_at=now() WHERE id=? AND lease_token=?`,
		e.table(),
		status,
		message,
		c.ID,
		lease,
	)
	if saveErr != nil {
		return nil, saveErr
	}
	c, err = e.load(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	return &av.ExecuteCorrectionResponse{Correction: c.Proto()}, nil
}

func (e *Engine) reserve(parent, ctx context.Context, c *Case) error {
	var err error
	if !c.Reserved {
		err = e.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			if xerr := e.checkLease(ctx, tx, c, false); xerr != nil {
				return xerr
			}
			if _, xerr := admin.RequireCurrent(ctx, tx, false); xerr != nil {
				return xerr
			}
			if xerr := e.Reserve(ctx, tx, c); xerr != nil {
				return xerr
			}
			_, xerr := tx.ExecContext(
				ctx,
				`UPDATE ? SET reserved=true WHERE id=? AND lease_token=?`,
				e.table(),
				c.ID,
				c.LeaseToken,
			)
			return xerr
		})
		if err != nil {
			if saveErr := e.recordReserveError(context.WithoutCancel(parent), c, err); saveErr != nil {
				return saveErr
			}
			return err
		}
		c.Reserved = true
	}
	return nil
}

func (e *Engine) executionOutcome(ctx context.Context, execution *Execution, err error) (string, string) {
	status, message := "completed", ""
	if err != nil {
		status = "retrying"
		message = PublicError(err)
		if execution.Case.Attempts >= 5 || connect.CodeOf(err) == connect.CodeFailedPrecondition ||
			connect.CodeOf(err) == connect.CodePermissionDenied ||
			connect.CodeOf(err) == connect.CodeAborted {
			status = "needs_attention"
		}
		if (connect.CodeOf(err) == connect.CodeAborted || connect.CodeOf(err) == connect.CodeFailedPrecondition) &&
			!execution.hasCompletedStep() {
			if releaseErr := e.releaseResources(context.WithoutCancel(ctx), execution.Case); releaseErr == nil {
				status = "failed"
			}
		}
	}
	return status, message
}

func validateExecution(c *Case, actorID int64, req *av.ExecuteCorrectionRequest) error {
	if c.Digest != req.Digest {
		return Failure(connect.CodeAborted, "纠错预览已变化")
	}
	if c.Status == "preview" && c.ActorID != actorID {
		return Failure(connect.CodePermissionDenied, "请先由本人生成纠错预览")
	}
	if c.Status == "preview" && time.Now().After(c.ExpiresAt) {
		return Failure(connect.CodeAborted, "预览已过期，请重新核查")
	}
	if c.RequestID != "" && c.RequestID != req.RequestId {
		return Failure(connect.CodeAlreadyExists, "该纠错已提交，请使用原请求编号查询或继续")
	}
	if c.Status == "failed" {
		return Failure(connect.CodeFailedPrecondition, "预览条件已失效，请重新生成预览")
	}
	return nil
}

func PublicError(err error) string {
	var rpc *connect.Error
	if errors.As(err, &rpc) {
		return rpc.Message()
	}
	return "处理暂未完成，可查看当前步骤后重试"
}

type Execution struct {
	Engine *Engine
	Case   *Case
	Lease  string
}

func (x *Execution) hasCompletedStep() bool {
	for _, s := range x.Case.Steps {
		if s.Status == "completed" {
			return true
		}
	}
	return false
}

func (e *Engine) releaseResources(ctx context.Context, c *Case) error {
	tables := map[string][]string{"spot": {"spot.spot_order"}, "payment": {"payment.payment_bill"}, "errand": {"errand.errand_task", "errand.errand_demand"}, "westpocket": {"westpocket.pocket"}}[e.Domain]
	return e.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := e.checkLease(ctx, tx, c, true); err != nil {
			return err
		}
		var completed bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM jsonb_array_elements(steps) s WHERE s->>'status'='completed') FROM ? WHERE id=?`, e.table(), c.ID).
			Scan(ctx, &completed); err != nil {
			return err
		}
		if completed {
			return Failure(connect.CodeFailedPrecondition, "已有已执行步骤，不能释放纠错资源")
		}
		if _, err := tx.ExecContext(ctx, `SELECT set_config('sast.admin_operation_id',?,true)`, c.ID); err != nil {
			return err
		}
		if e.RollbackReserve != nil {
			if err := e.RollbackReserve(ctx, tx, c); err != nil {
				return err
			}
		}
		for _, table := range tables {
			if _, err := tx.ExecContext(
				ctx,
				`UPDATE ? SET admin_operation_id='' WHERE admin_operation_id=?`,
				bun.Ident(table),
				c.ID,
			); err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(
			ctx,
			`UPDATE ? SET status='failed',reserved=false WHERE id=? AND lease_token=?`,
			e.table(),
			c.ID,
			c.LeaseToken,
		)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return Failure(connect.CodeAborted, "操作执行权已变更")
		}
		return nil
	})
}

func (e *Engine) checkLease(ctx context.Context, tx bun.Tx, c *Case, requireReserved bool) error {
	var valid bool
	if err := tx.NewRaw(`SELECT lease_token=? AND lease_until>now() AND status='executing' AND (NOT ? OR reserved) FROM ? WHERE id=? FOR UPDATE`, c.LeaseToken, requireReserved, e.table(), c.ID).
		Scan(ctx, &valid); err != nil {
		return err
	}
	if !valid {
		return Failure(connect.CodeAborted, "操作执行权已变更，请刷新进度")
	}
	return nil
}

func (x *Execution) result(key string) (json.RawMessage, bool) {
	for _, step := range x.Case.Steps {
		if step.Key == key && step.Status == "completed" {
			return step.Result, true
		}
	}
	return nil, false
}

func (x *Execution) saveStep(ctx context.Context, db bun.IDB, key string, result any) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	found := false
	for i := range x.Case.Steps {
		if x.Case.Steps[i].Key == key {
			x.Case.Steps[i].Status = "completed"
			x.Case.Steps[i].Result = data
			x.Case.Steps[i].Error = ""
			found = true
		}
	}
	if !found {
		return errors.New("unknown correction step")
	}
	steps, err := json.Marshal(x.Case.Steps)
	if err != nil {
		return err
	}
	res, err := db.ExecContext(
		ctx,
		`UPDATE ? SET steps=?::jsonb,updated_at=now() WHERE id=? AND lease_token=? AND lease_until>now()`,
		x.Engine.table(),
		string(steps),
		x.Case.ID,
		x.Lease,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return Failure(connect.CodeAborted, "操作执行权已变更，请刷新进度")
	}
	return nil
}

func (x *Execution) RemoteStep(ctx context.Context, key string, fn func(context.Context) (any, error), out any) error {
	if data, ok := x.result(key); ok {
		if out != nil {
			return json.Unmarshal(data, out)
		}
		return nil
	}
	value, err := fn(ctx)
	if err != nil {
		return err
	}
	if err = x.saveStep(ctx, x.Engine.DB, key, value); err != nil {
		return err
	}
	if out != nil {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, out)
	}
	return nil
}

func (x *Execution) LocalStep(ctx context.Context, key string, fn func(context.Context, bun.Tx) (any, error)) error {
	if _, ok := x.result(key); ok {
		return nil
	}
	return x.Engine.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := admin.RequireCurrent(ctx, tx, false); err != nil {
			return err
		}
		var valid bool
		if err := tx.NewRaw(`SELECT lease_token=? AND lease_until>now() FROM ? WHERE id=? FOR UPDATE`, x.Lease, x.Engine.table(), x.Case.ID).
			Scan(ctx, &valid); err != nil {
			return err
		}
		if !valid {
			return Failure(connect.CodeAborted, "操作执行权已变更")
		}
		if _, err := tx.ExecContext(ctx, `SELECT set_config('sast.admin_operation_id',?,true)`, x.Case.ID); err != nil {
			return err
		}
		value, err := fn(ctx, tx)
		if err != nil {
			return err
		}
		return x.saveStep(ctx, tx, key, value)
	})
}

// Run resumes durable operations after process restarts; unprivileged actors require manual takeover.
func (e *Engine) Run(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.resume(ctx)
		}
	}
}

func (e *Engine) resume(ctx context.Context) {
	var cases []Case
	err := e.DB.NewSelect().
		TableExpr("?", e.table()).
		Column("*").
		Where("status IN ('executing','retrying') AND next_attempt_at<=now() AND (lease_until IS NULL OR lease_until<now())").
		OrderExpr("created_at").
		Limit(10).
		Scan(ctx, &cases)
	if err != nil {
		return
	}
	for _, c := range cases {
		var u interceptor.AuthUser
		err = e.DB.NewRaw(`SELECT id AS user_id,role,status,auth_version FROM "user".user_account WHERE id=?`, c.ExecutorID).
			Scan(ctx, &u)
		if err != nil || u.Status != "active" || (u.Role != "admin" && u.Role != "superadmin") {
			_, updateErr := e.DB.ExecContext(
				ctx,
				`UPDATE ? SET status='needs_attention',error='原执行人权限已变更，需要管理员接管',updated_at=now() WHERE id=? AND (lease_until IS NULL OR lease_until<now())`,
				e.table(),
				c.ID,
			)
			if updateErr != nil {
				log.Error().Err(updateErr).Str("domain", e.Domain).Msg("persist correction takeover state")
			}
			continue
		}
		actorCtx := interceptor.SetUserToContext(ctx, &u)
		if _, executeErr := e.Execute(
			actorCtx,
			&av.ExecuteCorrectionRequest{PreviewId: c.ID, Digest: c.Digest, RequestId: c.RequestID},
		); executeErr != nil {
			log.Warn().
				Str("domain", e.Domain).
				Str("case_id", c.ID).
				Str("code", connect.CodeOf(executeErr).String()).
				Msg("resume correction deferred")
		}
	}
}

// A failed COMMIT acknowledgement can still mean the reservation committed.
// Consult durable reserved state and keep unknown outcomes resumable.
func (e *Engine) recordReserveError(ctx context.Context, c *Case, cause error) error {
	retryStatus := "retrying"
	if c.Attempts >= 5 {
		retryStatus = "needs_attention"
	}
	rejectedStatus := retryStatus
	switch connect.CodeOf(cause) {
	case connect.CodeAborted,
		connect.CodeFailedPrecondition,
		connect.CodePermissionDenied,
		connect.CodeUnauthenticated,
		connect.CodeInvalidArgument:
		rejectedStatus = "failed"
	}
	_, err := e.DB.ExecContext(
		ctx,
		`UPDATE ? SET status=CASE WHEN reserved THEN ? ELSE ? END,error=?,lease_until=NULL,next_attempt_at=now()+interval '30 seconds',updated_at=now() WHERE id=? AND lease_token=?`,
		e.table(),
		retryStatus,
		rejectedStatus,
		PublicError(cause),
		c.ID,
		c.LeaseToken,
	)
	return err
}
