package westpocket

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/adminflow"
	"github.com/uptrace/bun"
)

func (s *Service) AdminEngine() *adminflow.Engine {
	return &adminflow.Engine{
		DB:      s.DB,
		Domain:  "westpocket",
		Build:   s.adminBuildCorrection,
		Reserve: s.adminReserveCorrection,
		Apply:   s.adminApplyCorrection,
	}
}

//nolint:gocyclo // Freeze the full membership and payment graph before any correction is reserved.
func (s *Service) adminBuildCorrection(ctx context.Context, r *av.PreviewCorrectionRequest) (*adminflow.Plan, error) {
	if r.Resource != av.ResourceKind_RESOURCE_KIND_POCKET {
		return nil, failure(connect.CodeInvalidArgument, "仅活动支持整单纠错")
	}
	id, e := admin.ParseID(r.Id)
	if e != nil {
		return nil, e
	}
	for key := range r.Values {
		if key != "funds_handling" && key != "missing_bill_confirmed" && key != "submitted_unreceived_confirmed" {
			return nil, failure(connect.CodeInvalidArgument, "存在不支持的活动恢复字段")
		}
	}
	funds := r.Values["funds_handling"]
	if funds != "no_payment_received" && funds != "refunded" && funds != "retain" && funds != "confirmation_error" {
		return nil, failure(connect.CodeInvalidArgument, "请确认原付款的处理方式")
	}
	p, e := s.adminReadPocket(ctx, s.DB, id, false)
	if e != nil {
		return nil, e
	}
	if p.AdminOperationID != "" || p.LifecycleRound != r.ExpectedRound ||
		!p.UpdatedAt.Equal(r.ExpectedUpdatedAt.AsTime()) {
		return nil, ErrConflict
	}
	snap := adminPocketSnapshot{Pocket: *p}
	if e = s.DB.NewSelect().
		TableExpr("westpocket.pocket_member").
		Column("id", "user_id", "share_cents", "payment_bill_id", "bill_status").
		Where("pocket_id=?", id).
		Order("id").
		Scan(ctx, &snap.Members); e != nil {
		return nil, e
	}
	snap.Bills, e = adminflow.GetSourceBills(ctx, "west_pocket", id, p.LifecycleRound)
	if e != nil {
		return nil, e
	}
	switch r.Action {
	case "cancel":
		if p.Status == "cancelled" {
			return nil, ErrState
		}
		snap.TargetStatus = "cancelled"
	case "reverse_completion":
		if p.ReceivableCents <= 0 {
			return nil, failure(connect.CodeFailedPrecondition, "本活动无应收款，不适用撤销收款；撤销整单请使用取消")
		}
		if p.Status != "settled" {
			return nil, ErrState
		}
		if funds != "refunded" && funds != "confirmation_error" {
			return nil, failure(connect.CodeInvalidArgument, "撤销活动结算必须选择已核验退款或原收款确认有误；保留全部收款无法撤销AA结算")
		}
		snap.TargetStatus = "collecting"
	case "reopen":
		if p.Status != "cancelled" {
			return nil, ErrState
		}
		snap.TargetStatus = "collecting"
		if p.PublishedAt == nil && len(snap.Bills) == 0 {
			snap.TargetStatus = "draft"
		}
	default:
		return nil, failure(connect.CodeInvalidArgument, "不支持的活动纠错动作")
	}
	if e = validatePocketCorrectionMembers(snap, r.Values["missing_bill_confirmed"] == "true"); e != nil {
		return nil, e
	}
	if snap.TargetStatus == "collecting" {
		byPayer := map[string]bool{}
		for _, bill := range snap.Bills {
			byPayer[bill.PayerId] = true
		}
		for _, m := range snap.Members {
			if m.UserID != p.OwnerID && m.ShareCents > 0 && !byPayer[fmt.Sprint(m.UserID)] {
				snap.RestorationBills = append(
					snap.RestorationBills,
					&av.SourceBill{
						PayerId:     fmt.Sprint(m.UserID),
						PayeeId:     fmt.Sprint(p.OwnerID),
						AmountCents: int64(m.ShareCents),
					},
				)
			}
		}
	}
	if e = admin.ValidateSourceFunds(
		r.Action,
		funds,
		snap.Bills,
		r.Values["submitted_unreceived_confirmed"] == "true",
	); e != nil {
		return nil, e
	}
	raw, e := json.Marshal(snap)
	if e != nil {
		return nil, e
	}
	return &adminflow.Plan{
		Record:   pocketAdminRecord(p),
		Snapshot: raw,
		Steps: []adminflow.Step{
			{Key: "payment", Description: "核验并纠正整组账单", Status: "pending"},
			{Key: "pocket", Description: "恢复活动原编号及冻结分摊", Status: "pending"},
		},
		Impacts: []*av.Impact{
			{
				Resource:    r.Resource,
				ResourceId:  r.Id,
				Description: "活动状态与生命周期",
				Before:      fmt.Sprintf("%s / 第%d轮", p.Status, p.LifecycleRound),
				After:       fmt.Sprintf("%s / 第%d轮", snap.TargetStatus, p.LifecycleRound+1),
			},
			{
				Resource:    av.ResourceKind_RESOURCE_KIND_BILL,
				ResourceId:  r.Id,
				Description: "所有关联账单统一换轮次，保留原付款和退款记录",
				Before:      fmt.Sprintf("%d 张", len(snap.Bills)),
				After:       funds,
			},
		},
		Warnings: []string{
			"仅登记已核实的线下资金事实，不会自动发起转账或退款", "申报付款须逐笔核实；实际已到账应先确认收款再整单纠错",
			"保留原成员与分摊金额；旧轮次任务和通知失效",
			"不会恢复照片、人脸数据或已撤回的授权",
			"撤销已结算活动仅支持已核验退款或原收款确认有误",
		},
	}, nil
}

//nolint:gocyclo // Validate all AA membership, ownership, amount and billing invariants together.
func validatePocketCorrectionMembers(snap adminPocketSnapshot, allowMissing bool) error {
	if snap.TargetStatus == "draft" || snap.TargetStatus == "cancelled" {
		return nil
	}
	if len(snap.Members) != int(snap.Pocket.ParticipantCount) || len(snap.Members) == 0 {
		return failure(connect.CodeFailedPrecondition, "活动成员快照不完整，不能恢复收款")
	}
	byUser := map[int64]adminPocketMember{}
	var total int64
	for _, m := range snap.Members {
		if m.UserID <= 0 || m.ShareCents < 0 {
			return ErrState
		}
		if _, ok := byUser[m.UserID]; ok {
			return ErrState
		}
		byUser[m.UserID] = m
		total += int64(m.ShareCents)
	}
	owner, exists := byUser[snap.Pocket.OwnerID]
	if !exists || total != int64(snap.Pocket.TotalCents) || owner.ShareCents != snap.Pocket.OwnerShareCents ||
		total-int64(owner.ShareCents) != int64(snap.Pocket.ReceivableCents) {
		return failure(connect.CodeFailedPrecondition, "原始分摊总额不一致，不能恢复")
	}
	found := map[int64]bool{}
	for _, bill := range snap.Bills {
		payer, e := strconv.ParseInt(bill.PayerId, 10, 64)
		if e != nil {
			return ErrState
		}
		member, ok := byUser[payer]
		if !ok || payer == snap.Pocket.OwnerID || bill.PayeeId != fmt.Sprint(snap.Pocket.OwnerID) ||
			bill.AmountCents != int64(member.ShareCents) ||
			found[payer] {
			return failure(connect.CodeFailedPrecondition, "账单与冻结分摊不一致")
		}
		found[payer] = true
	}
	for id, m := range byUser {
		if id != snap.Pocket.OwnerID && m.ShareCents > 0 && !found[id] && !allowMissing {
			return failure(connect.CodeFailedPrecondition, "存在缺失账单，请核验原份额后勾选补建缺失账单")
		}
	}
	return nil
}

func (s *Service) adminReserveCorrection(ctx context.Context, tx bun.Tx, c *adminflow.Case) error {
	if _, e := tx.ExecContext(
		ctx,
		"SELECT pg_advisory_xact_lock(hashtextextended(?,1))",
		fmt.Sprintf("wp-pocket:%d", c.ResourceID),
	); e != nil {
		return e
	}
	p, e := s.adminReadPocket(ctx, tx, c.ResourceID, true)
	if e != nil {
		return e
	}
	if p.AdminOperationID != "" || p.LifecycleRound != c.Plan.Request.ExpectedRound ||
		!p.UpdatedAt.Equal(c.Plan.Request.ExpectedUpdatedAt.AsTime()) {
		return ErrConflict
	}
	var snap adminPocketSnapshot
	if e = json.Unmarshal(c.Plan.Snapshot, &snap); e != nil {
		return e
	}
	var currentMembers []adminPocketMember
	if e = tx.NewSelect().
		TableExpr("westpocket.pocket_member").
		Column("id", "user_id", "share_cents", "payment_bill_id", "bill_status").
		Where("pocket_id=?", p.ID).
		Order("id").
		For("UPDATE").
		Scan(ctx, &currentMembers); e != nil {
		return e
	}
	current, e := json.Marshal(currentMembers)
	if e != nil {
		return e
	}
	expected, e := json.Marshal(snap.Members)
	if e != nil {
		return e
	}
	if string(current) != string(expected) {
		return ErrConflict
	}
	if _, e = tx.ExecContext(ctx, "SELECT set_config('sast.admin_operation_id',?,true)", c.ID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(
		ctx,
		`UPDATE westpocket.pocket SET admin_operation_id=? WHERE id=?`,
		c.ID,
		p.ID,
	); e != nil {
		return e
	}
	return nil
}

func cancelOldPocketWork(ctx context.Context, tx bun.Tx, p *adminPocket) error {
	var e error
	if _, e = tx.ExecContext(
		ctx,
		`UPDATE westpocket.async_job SET status='cancelled',lease_expires_at=NULL,updated_at=now() WHERE pocket_id=? AND lifecycle_round=? AND kind IN ('publish','cancel','recognize') AND status IN ('queued','running','failed','partial_failed')`,
		p.ID,
		p.LifecycleRound,
	); e != nil {
		return e
	}
	_, e = tx.ExecContext(
		ctx,
		`UPDATE westpocket.notification_outbox SET status='cancelled',lease_expires_at=NULL,updated_at=now() WHERE pocket_id=? AND lifecycle_round=? AND status IN ('pending','retrying','unknown','failed','sending')`,
		p.ID,
		p.LifecycleRound,
	)
	return e
}

//nolint:gocyclo // Persist bill bindings, source lifecycle and immutable history atomically.
func (s *Service) adminApplyCorrection(ctx context.Context, x *adminflow.Execution) error {
	var snap adminPocketSnapshot
	if e := json.Unmarshal(x.Case.Plan.Snapshot, &snap); e != nil {
		return e
	}
	next := snap.Pocket.LifecycleRound + 1
	var payment av.CorrectSourceBillsResponse
	if e := x.RemoteStep(ctx, "payment", func(ctx context.Context) (any, error) {
		return adminflow.CorrectBills(
			ctx,
			x.Case,
			"west_pocket",
			next,
			snap.Bills,
			x.Case.Plan.Request.Values["funds_handling"],
			snap.RestorationBills,
		)
	}, &payment); e != nil {
		return e
	}
	return x.LocalStep(ctx, "pocket", func(ctx context.Context, tx bun.Tx) (any, error) {
		p, e := s.adminReadPocket(ctx, tx, x.Case.ResourceID, true)
		if e != nil {
			return nil, e
		}
		if p.AdminOperationID != x.Case.ID || p.LifecycleRound != snap.Pocket.LifecycleRound {
			return nil, ErrConflict
		}
		if e = cancelOldPocketWork(ctx, tx, p); e != nil {
			return nil, e
		}
		if _, e = tx.ExecContext(
			ctx,
			`INSERT INTO westpocket.admin_round_history(resource,resource_id,lifecycle_round,operation_id,snapshot) VALUES ('pocket',?,?,?,?::jsonb)`,
			p.ID,
			p.LifecycleRound,
			x.Case.ID,
			string(x.Case.Plan.Snapshot),
		); e != nil {
			return nil, e
		}
		for _, m := range snap.Members {
			var billID *int64
			status := "self_share"
			if m.UserID != p.OwnerID {
				status = "unpaid"
				if snap.TargetStatus == "cancelled" {
					status = "closed"
				}
				for _, bill := range payment.Bills {
					if bill.PayerId == fmt.Sprint(m.UserID) {
						id, e := admin.ParseID(bill.Id)
						if e != nil {
							return nil, e
						}
						billID = &id
						status = bill.Status
						break
					}
				}
				if snap.TargetStatus == "collecting" && m.ShareCents > 0 && billID == nil {
					return nil, failure(connect.CodeFailedPrecondition, "恢复后的成员账单不完整")
				}
			}
			if _, e = tx.ExecContext(
				ctx,
				`UPDATE westpocket.pocket_member SET payment_bill_id=?,bill_status=?,last_reminded_at=NULL,updated_at=now() WHERE id=? AND pocket_id=?`,
				billID,
				status,
				m.ID,
				p.ID,
			); e != nil {
				return nil, e
			}
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		var cancelledAt *time.Time
		if snap.TargetStatus == "cancelled" {
			cancelledAt = &now
		}
		if _, e = tx.ExecContext(
			ctx,
			`UPDATE westpocket.pocket SET status=?,lifecycle_round=?,revision=revision+1,admin_operation_id='',settled_at=NULL,cancelled_at=?,cancel_reason=?,updated_at=? WHERE id=?`,
			snap.TargetStatus,
			next,
			cancelledAt,
			string([]rune(x.Case.Plan.Request.Reason)[:min(len([]rune(x.Case.Plan.Request.Reason)), 500)]),
			now,
			p.ID,
		); e != nil {
			return nil, e
		}
		if e = admin.WriteAudit(
			ctx,
			tx,
			admin.Entry{
				Domain:     "westpocket",
				ActorID:    x.Case.ExecutorID,
				Action:     x.Case.Action,
				TargetType: "pocket",
				TargetID:   p.ID,
				RequestID:  x.Case.RequestID,
				Reason:     x.Case.Plan.Request.Reason,
				Evidence:   x.Case.Plan.Request.Evidence,
				Before: map[string]any{
					"status":          p.Status,
					"lifecycle_round": p.LifecycleRound,
					"total_cents":     p.TotalCents,
				},
				After: map[string]any{
					"status":          snap.TargetStatus,
					"lifecycle_round": next,
					"total_cents":     p.TotalCents,
				},
			},
		); e != nil {
			return nil, e
		}
		return map[string]any{"lifecycle_round": next, "status": snap.TargetStatus}, nil
	})
}
