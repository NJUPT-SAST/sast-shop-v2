package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/adminflow"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/model"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func AdminListBills(ctx context.Context, req *av.ListRecordsRequest) (*av.ListRecordsResponse, error) {
	if _, err := admin.Require(ctx); err != nil {
		return nil, err
	}
	if req.Resource != av.ResourceKind_RESOURCE_KIND_BILL {
		return nil, adminflow.Failure(connect.CodeInvalidArgument, "资源类型无效")
	}
	page, size, err := admin.Page(req.Page, req.PageSize)
	if err != nil {
		return nil, err
	}
	if err = admin.TimeRange(req.CreatedFrom, req.CreatedTo); err != nil {
		return nil, err
	}
	if len(req.Keyword) > 200 {
		return nil, adminflow.Failure(connect.CodeInvalidArgument, "搜索内容过长")
	}
	var bills []*model.PaymentBill
	query := postgres.DB.NewSelect().Model(&bills)
	if req.Keyword != "" {
		pattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(req.Keyword) + "%"
		query = query.Where("(pb.bill_no ILIKE ? ESCAPE '!' OR pb.id::text=?)", pattern, req.Keyword)
	}
	query, err = filterBillStatus(query, req.Status)
	if err != nil {
		return nil, err
	}

	if req.UserId != "" {
		id, err := admin.ParseID(req.UserId)
		if err != nil {
			return nil, err
		}
		query = query.Where("(pb.payer_id=? OR pb.payee_id=?)", id, id)
	}
	if req.CreatedFrom != nil {
		query = query.Where("pb.created_at>=?", req.CreatedFrom.AsTime())
	}
	if req.CreatedTo != nil {
		query = query.Where("pb.created_at<=?", req.CreatedTo.AsTime())
	}
	count, err := query.OrderExpr("pb.created_at DESC,pb.id DESC").
		Limit(int(size)).
		Offset(int((page - 1) * size)).
		ScanAndCount(ctx)
	if err != nil {
		return nil, err
	}
	out := &av.ListRecordsResponse{TotalCount: int64(count), Page: page, PageSize: size}
	for _, bill := range bills {
		record, err := adminBillRecord(ctx, postgres.DB, bill)
		if err != nil {
			return nil, err
		}
		out.Records = append(out.Records, record)
	}
	return out, nil
}

func AdminGetBill(ctx context.Context, req *av.GetRecordRequest) (*av.GetRecordResponse, error) {
	if _, err := admin.Require(ctx); err != nil {
		return nil, err
	}
	if req.Resource != av.ResourceKind_RESOURCE_KIND_BILL {
		return nil, adminflow.Failure(connect.CodeInvalidArgument, "资源类型无效")
	}
	id, err := admin.ParseID(req.Id)
	if err != nil {
		return nil, err
	}
	var bill model.PaymentBill
	err = postgres.DB.NewSelect().Model(&bill).Where("id=?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	record, err := adminBillRecord(ctx, postgres.DB, &bill)
	if err != nil {
		return nil, err
	}
	out := &av.GetRecordResponse{Record: record}
	if bill.SourceType != nil && bill.SourceID != nil {
		var related []*model.PaymentBill
		err = postgres.DB.NewSelect().
			Model(&related).
			Where("source_type=? AND source_id=? AND payer_id=? AND id<>?", *bill.SourceType, *bill.SourceID, bill.PayerID, bill.ID).
			OrderExpr("lifecycle_round DESC,id DESC").
			Limit(100).
			Scan(ctx)
		if err != nil {
			return nil, err
		}
		for _, row := range related {
			record, err := adminBillRecord(ctx, postgres.DB, row)
			if err != nil {
				return nil, err
			}
			out.RelatedRecords = append(out.RelatedRecords, record)
		}
	}
	return out, nil
}

func adminBillRecord(ctx context.Context, db bun.IDB, bill *model.PaymentBill) (*av.AdminRecord, error) {
	funding := bill.ID
	if bill.FundingBillID != nil {
		funding = *bill.FundingBillID
	}
	refunded, err := db.NewSelect().TableExpr("payment.admin_refund").Where("bill_id=?", funding).Exists(ctx)
	if err != nil {
		return nil, err
	}
	reversed, err := db.NewSelect().
		TableExpr("payment.admin_confirmation_reversal").
		Where("bill_id=?", funding).
		Exists(ctx)
	if err != nil {
		return nil, err
	}
	amount := int64(bill.AmountCents)
	record := &av.AdminRecord{
		Resource:       av.ResourceKind_RESOURCE_KIND_BILL,
		Id:             fmt.Sprint(bill.ID),
		Title:          bill.BillNo,
		Status:         string(bill.Status),
		OwnerId:        fmt.Sprint(bill.PayerID),
		AmountCents:    &amount,
		LifecycleRound: bill.LifecycleRound,
		CreatedAt:      timestamppb.New(bill.CreatedAt),
		UpdatedAt:      timestamppb.New(bill.UpdatedAt),
	}
	field := func(key, label, value string) {
		record.Fields = append(record.Fields, &av.RecordField{Key: key, Label: label, Value: value})
	}
	field("payer_id", "付款人", fmt.Sprint(bill.PayerID))
	field("payee_id", "收款人", fmt.Sprint(bill.PayeeID))
	field("serial_number", "付款备注", bill.SerialNumber)
	field("refund_registered", "全额退款已核实", fmt.Sprint(refunded))
	field("confirmation_reversed", "原收款确认已纠正", fmt.Sprint(reversed))
	field("superseded", "历史轮次账单", fmt.Sprint(bill.SupersededAt != nil))
	if bill.SourceType != nil {
		field("source_type", "业务来源", *bill.SourceType)
	}
	if bill.SourceID != nil {
		field("source_id", "业务编号", fmt.Sprint(*bill.SourceID))
	}
	if bill.AdminOperationID != "" {
		field("correction_id", "正在处理的纠错", bill.AdminOperationID)
	}
	if bill.SupersededAt == nil && bill.AdminOperationID == "" && !refunded && !reversed {
		switch bill.Status {
		case model.PaymentBillStatusSubmitted:
			record.AllowedActions = []string{"reject_payment", "confirm_payment"}
		case model.PaymentBillStatusCompleted:
			if bill.SourceType == nil && bill.SourceID == nil {
				record.AllowedActions = []string{"register_refund"}
			} else {
				field("refund_via_source", "全额退款", "请进入关联交易执行整单纠错，联动更新履约与账单状态")
			}
		}
	}
	return record, nil
}

type billCorrectionSnapshot struct {
	Bill model.PaymentBill `json:"bill"`
}

func AdminPaymentEngine() *adminflow.Engine {
	engine := &adminflow.Engine{DB: postgres.DB, Domain: "payment"}
	engine.Build = buildBillCorrection
	engine.Reserve = reserveBillCorrection
	engine.Apply = applyBillCorrection
	return engine
}

func AdminPaymentStats(ctx context.Context) (*av.GetStatsResponse, error) {
	if _, err := admin.Require(ctx); err != nil {
		return nil, err
	}
	var totals struct{ Total, Pending, Confirmed, Refunded int64 }
	err := postgres.DB.NewRaw(`SELECT count(*) FILTER(WHERE superseded_at IS NULL) AS total,
 count(*) FILTER(WHERE superseded_at IS NULL AND status='submitted') AS pending,
 COALESCE(sum(amount_cents) FILTER(WHERE superseded_at IS NULL AND status='completed' AND NOT EXISTS(SELECT 1 FROM payment.admin_refund r WHERE r.bill_id=COALESCE(payment_bill.funding_bill_id,payment_bill.id))),0) AS confirmed,
 (SELECT COALESCE(sum(amount_cents),0) FROM payment.admin_refund) AS refunded FROM payment.payment_bill`).Scan(ctx, &totals)
	if err != nil {
		return nil, err
	}
	return &av.GetStatsResponse{
		Metrics: []*av.Metric{
			{Key: "bills", Label: "当前账单", Value: totals.Total},
			{Key: "bills_pending", Label: "待核实付款", Value: totals.Pending},
			{Key: "confirmed_cents", Label: "当前有效确认收款", Value: totals.Confirmed, IsMoney: true},
			{Key: "refunded_cents", Label: "累计核实全额退款", Value: totals.Refunded, IsMoney: true},
		},
	}, nil
}

func AdminPaymentError(err error) error {
	if err == nil {
		return nil
	}
	var rpc *connect.Error
	if errors.As(err, &rpc) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		return adminflow.Failure(connect.CodeNotFound, "账单不存在")
	}
	return adminflow.Failure(connect.CodeInternal, "账单管理操作暂未完成，请刷新核实")
}

func buildBillCorrection(ctx context.Context, req *av.PreviewCorrectionRequest) (*adminflow.Plan, error) {
	response, err := AdminGetBill(ctx, &av.GetRecordRequest{Resource: req.Resource, Id: req.Id})
	if err != nil {
		return nil, err
	}
	if req.ExpectedRound != response.Record.LifecycleRound ||
		!req.ExpectedUpdatedAt.AsTime().Equal(response.Record.UpdatedAt.AsTime()) {
		return nil, adminflow.Failure(connect.CodeAborted, "账单已变化，请刷新")
	}
	if err = validateBillCorrectionAction(req, response.Record); err != nil {
		return nil, err
	}
	id, err := admin.ParseID(req.Id)
	if err != nil {
		return nil, err
	}
	var bill model.PaymentBill
	if err = postgres.DB.NewSelect().Model(&bill).Where("id=?", id).Scan(ctx); err != nil {
		return nil, err
	}
	if bill.LifecycleRound != req.ExpectedRound || !bill.UpdatedAt.Equal(req.ExpectedUpdatedAt.AsTime()) ||
		bill.AdminOperationID != "" ||
		bill.SupersededAt != nil {
		return nil, adminflow.Failure(connect.CodeAborted, "账单已变化，请刷新")
	}
	if req.Action != "register_refund" {
		if err = requireWestPocketCollecting(ctx, &bill); err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(billCorrectionSnapshot{Bill: bill})
	if err != nil {
		return nil, err
	}
	after := map[string]string{"reject_payment": "待付款", "confirm_payment": "已确认收款", "register_refund": "全额退款已核实"}[req.Action]
	return &adminflow.Plan{
		Record:   response.Record,
		Snapshot: data,
		Steps:    []adminflow.Step{{Key: "bill", Description: "更新账单并保存核实记录", Status: "pending"}},
		Impacts: []*av.Impact{
			{
				Resource:    req.Resource,
				ResourceId:  req.Id,
				Description: "整张账单",
				Before:      response.Record.Status,
				After:       after,
			},
		},
		Warnings: []string{"系统不会执行实际转账；退款需已在线下完成并核实"},
	}, nil
}

func reserveBillCorrection(ctx context.Context, tx bun.Tx, c *adminflow.Case) error {
	var snapshot billCorrectionSnapshot
	if err := json.Unmarshal(c.Plan.Snapshot, &snapshot); err != nil {
		return err
	}
	var bill model.PaymentBill
	if err := tx.NewSelect().Model(&bill).Where("id=?", c.ResourceID).For("UPDATE").Scan(ctx); err != nil {
		return err
	}
	if !bill.UpdatedAt.Equal(snapshot.Bill.UpdatedAt) || bill.LifecycleRound != snapshot.Bill.LifecycleRound ||
		bill.AdminOperationID != "" ||
		bill.SupersededAt != nil {
		return adminflow.Failure(connect.CodeAborted, "账单已变化，请重新预览")
	}
	_, err := tx.NewUpdate().
		Model((*model.PaymentBill)(nil)).
		Set("admin_operation_id=?", c.ID).
		Where("id=?", bill.ID).
		Exec(ctx)
	return err
}

func applyBillCorrection(ctx context.Context, x *adminflow.Execution) error {
	return x.LocalStep(ctx, "bill", func(ctx context.Context, tx bun.Tx) (any, error) {
		actor, err := admin.RequireCurrent(ctx, tx, false)
		if err != nil {
			return nil, err
		}
		var bill model.PaymentBill
		if err = tx.NewSelect().Model(&bill).Where("id=?", x.Case.ResourceID).For("UPDATE").Scan(ctx); err != nil {
			return nil, err
		}
		if bill.AdminOperationID != x.Case.ID {
			return nil, adminflow.Failure(connect.CodeAborted, "账单纠错标记不匹配")
		}
		before := bill
		now := time.Now()
		if err = changeBillStatus(ctx, tx, x.Case, &bill, actor.UserID, now); err != nil {
			return nil, err
		}

		bill.AdminOperationID = ""
		bill.UpdatedAt = now
		if _, err = tx.NewUpdate().Model(&bill).WherePK().Exec(ctx); err != nil {
			return nil, err
		}
		if x.Case.Action == "confirm_payment" && bill.SourceType != nil && *bill.SourceType == "errand_task" {
			if _, err = tx.ExecContext(
				ctx,
				`INSERT INTO payment.business_outbox(event_key,bill_id,lifecycle_round)VALUES(?,?,?)ON CONFLICT(event_key)DO NOTHING`,
				fmt.Sprintf("confirmed:%d:%d", bill.ID, bill.LifecycleRound),
				bill.ID,
				bill.LifecycleRound,
			); err != nil {
				return nil, err
			}
		}
		err = admin.WriteAudit(
			ctx,
			tx,
			admin.Entry{
				Domain:     "payment",
				ActorID:    actor.UserID,
				Action:     x.Case.Action,
				TargetType: "bill",
				TargetID:   bill.ID,
				RequestID:  x.Case.RequestID,
				Reason:     x.Case.Plan.Request.Reason,
				Evidence:   x.Case.Plan.Request.Evidence,
				Before:     before,
				After:      bill,
			},
		)
		return map[string]any{"bill_id": bill.ID, "status": bill.Status}, err
	})
}

func filterBillStatus(query *bun.SelectQuery, status string) (*bun.SelectQuery, error) {
	if status == "" {
		return query, nil
	}

	switch status {
	case "unpaid", "submitted", "completed", "closed":
		query = query.Where("pb.status=?", status)
	case "superseded":
		query = query.Where("pb.superseded_at IS NOT NULL")
	case "refunded":
		query = query.Where(
			"EXISTS(SELECT 1 FROM payment.admin_refund r WHERE r.bill_id=COALESCE(pb.funding_bill_id,pb.id))",
		)
	default:
		return nil, adminflow.Failure(connect.CodeInvalidArgument, "账单状态无效")
	}

	return query, nil
}

func validateBillCorrectionAction(req *av.PreviewCorrectionRequest, record *av.AdminRecord) error {
	allowed := false
	for _, action := range record.AllowedActions {
		if req.Action == action {
			allowed = true
		}
	}
	if !allowed {
		return adminflow.Failure(connect.CodeFailedPrecondition, "账单当前状态不允许此操作")
	}
	for key := range req.Values {
		if key != "funds_handling" {
			return adminflow.Failure(connect.CodeInvalidArgument, "包含不支持的账单字段")
		}
	}
	if req.Action == "register_refund" && req.Values["funds_handling"] != "refunded" {
		return adminflow.Failure(connect.CodeInvalidArgument, "请确认已经完成全额退款")
	}
	return nil
}

func changeBillStatus(
	ctx context.Context,
	tx bun.Tx,
	c *adminflow.Case,
	bill *model.PaymentBill,
	actorID int64,
	now time.Time,
) error {
	var err error
	switch c.Action {
	case "reject_payment":
		if bill.Status != model.PaymentBillStatusSubmitted {
			return adminflow.Failure(connect.CodeFailedPrecondition, "账单不再处于待核实付款状态")
		}
		bill.Status = model.PaymentBillStatusUnpaid
		bill.SubmittedAt = nil
		bill.Channel = nil
	case "confirm_payment":
		if bill.Status != model.PaymentBillStatusSubmitted {
			return adminflow.Failure(connect.CodeFailedPrecondition, "账单不再处于待核实付款状态")
		}
		bill.Status = model.PaymentBillStatusCompleted
		bill.CompletedAt = &now
	case "register_refund":
		if bill.SourceType != nil || bill.SourceID != nil {
			return adminflow.Failure(connect.CodeFailedPrecondition, "请从关联交易执行整单退款纠错")
		}
		if err = registerFullRefund(
			ctx,
			tx,
			bill,
			actorID,
			c.ID,
			c.Plan.Request.Reason,
			c.Plan.Request.Evidence,
		); err != nil {
			return err
		}
	default:
		return adminflow.Failure(connect.CodeInvalidArgument, "纠错动作无效")
	}
	return nil
}
