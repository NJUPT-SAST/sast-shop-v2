package service

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

	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/adminflow"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/model"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func SourceAdminContext(ctx context.Context, actorID string) (context.Context, error) {
	id, err := admin.ParseID(actorID)
	if err != nil {
		return nil, err
	}
	var actor interceptor.AuthUser
	err = postgres.DB.NewRaw(`SELECT id AS user_id,role,status,auth_version FROM "user".user_account WHERE id=?`, id).
		Scan(ctx, &actor)
	if err != nil {
		return nil, adminflow.Failure(connect.CodePermissionDenied, "管理员身份不可用")
	}
	ctx = interceptor.SetUserToContext(ctx, &actor)
	if _, err = admin.Require(ctx); err != nil {
		return nil, err
	}
	return ctx, nil
}

func sourceTable(source string) (string, error) {
	switch source {
	case "spot_order":
		return "spot.spot_order", nil
	case "errand_task":
		return "errand.errand_task", nil
	case "west_pocket":
		return "westpocket.pocket", nil
	}
	return "", adminflow.Failure(connect.CodeInvalidArgument, "账单业务来源无效")
}

func AdminSourceBills(ctx context.Context, req *av.GetSourceBillsRequest) (*av.GetSourceBillsResponse, error) {
	if _, err := admin.Require(ctx); err != nil {
		return nil, err
	}
	if _, err := sourceTable(req.SourceType); err != nil {
		return nil, err
	}
	id, err := admin.ParseID(req.SourceId)
	if err != nil {
		return nil, err
	}
	if req.LifecycleRound < 1 {
		return nil, adminflow.Failure(connect.CodeInvalidArgument, "处理轮次无效")
	}
	var bills []*model.PaymentBill
	err = postgres.DB.NewSelect().
		Model(&bills).
		Where("source_type=? AND source_id=? AND lifecycle_round=?", req.SourceType, id, req.LifecycleRound).
		OrderExpr("id ASC").
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	records, err := sourceBillRecords(ctx, postgres.DB, bills)
	if err != nil {
		return nil, err
	}
	return &av.GetSourceBillsResponse{Bills: records}, nil
}

func sourceBillRecords(ctx context.Context, db bun.IDB, bills []*model.PaymentBill) ([]*av.SourceBill, error) {
	out := make([]*av.SourceBill, 0, len(bills))
	for _, b := range bills {
		if b.AdminOperationID != "" {
			return nil, adminflow.Failure(connect.CodeFailedPrecondition, "关联账单正在纠错，请完成后重新预览")
		}
		funding := b.ID
		if b.FundingBillID != nil {
			funding = *b.FundingBillID
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
		out = append(
			out,
			&av.SourceBill{
				Id:                   fmt.Sprint(b.ID),
				PayerId:              fmt.Sprint(b.PayerID),
				PayeeId:              fmt.Sprint(b.PayeeID),
				AmountCents:          int64(b.AmountCents),
				Status:               string(b.Status),
				UpdatedAt:            timestamppb.New(b.UpdatedAt),
				LifecycleRound:       b.LifecycleRound,
				RefundRegistered:     refunded,
				ConfirmationReversed: reversed,
				Superseded:           b.SupersededAt != nil,
			},
		)
	}
	return out, nil
}

func validateSourceSnapshot(actual []*model.PaymentBill, expected []*av.SourceBill) error {
	if len(actual) != len(expected) {
		return adminflow.Failure(connect.CodeAborted, "关联账单已经变化，请重新生成预览")
	}
	seen := map[int64]bool{}
	for _, want := range expected {
		id, err := admin.ParseID(want.Id)
		if err != nil {
			return err
		}
		if seen[id] {
			return adminflow.Failure(connect.CodeInvalidArgument, "关联账单重复")
		}
		seen[id] = true
		found := false
		for _, got := range actual {
			if got.ID == id {
				found = true
				if !sameSourceBillSnapshot(want, got) {
					return adminflow.Failure(connect.CodeAborted, "关联账单已经变化，请重新核实")
				}
			}
		}
		if !found {
			return adminflow.Failure(connect.CodeAborted, "关联账单已经变化")
		}
	}
	return nil
}

func registerFullRefund(
	ctx context.Context,
	tx bun.Tx,
	bill *model.PaymentBill,
	actorID int64,
	operation, reason, evidence string,
) error {
	if bill.AmountCents == 0 {
		return nil
	}
	if bill.Status != model.PaymentBillStatusCompleted {
		return adminflow.Failure(connect.CodeFailedPrecondition, "仅已确认收款的账单可以登记全额退款")
	}
	funding := bill.ID
	if bill.FundingBillID != nil {
		funding = *bill.FundingBillID
	}
	var original model.PaymentBill
	if err := tx.NewSelect().Model(&original).Where("id=?", funding).For("UPDATE").Scan(ctx); err != nil {
		return err
	}
	if original.AmountCents != bill.AmountCents || original.PayerID != bill.PayerID ||
		original.PayeeID != bill.PayeeID {
		return adminflow.Failure(connect.CodeFailedPrecondition, "历史付款关联不一致，需要核查")
	}
	reversed, err := tx.NewSelect().
		TableExpr("payment.admin_confirmation_reversal").
		Where("bill_id=?", funding).
		Exists(ctx)
	if err != nil {
		return err
	}
	if reversed {
		return adminflow.Failure(connect.CodeFailedPrecondition, "原收款确认已纠正，不能登记不存在的退款")
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO payment.admin_refund(bill_id,amount_cents,actor_id,operation_id,reason,evidence) VALUES (?,?,?,?,?,?) ON CONFLICT(bill_id) DO NOTHING`,
		funding,
		bill.AmountCents,
		actorID,
		operation,
		reason,
		evidence,
	)
	return err
}

func CorrectSourceBills(
	ctx context.Context,
	req *av.CorrectSourceBillsRequest,
) (*av.CorrectSourceBillsResponse, error) {
	sourceID, err := validateSourceCorrection(req)
	if err != nil {
		return nil, err
	}
	input, ok := proto.Clone(req).(*av.CorrectSourceBillsRequest)
	if !ok {
		return nil, adminflow.Failure(connect.CodeInternal, "纠错请求无法复制")
	}
	input.ActorId = ""
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(input)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	var result *av.CorrectSourceBillsResponse
	err = postgres.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		actor, xerr := admin.RequireCurrent(ctx, tx, false)
		if xerr != nil {
			return xerr
		}
		stored, replay, xerr := beginSourceCorrection(ctx, tx, req, hash)
		if xerr != nil {
			return xerr
		}
		if replay {
			result = stored
			return nil
		}
		if xerr = validateSourceReservation(ctx, tx, req, sourceID); xerr != nil {
			return xerr
		}
		bills, xerr := loadSourceCorrectionBills(ctx, tx, req, sourceID, actor.UserID)
		if xerr != nil {
			return xerr
		}
		next := make([]*model.PaymentBill, 0, len(bills))
		allPaid := len(bills) > 0
		for _, bill := range bills {
			b, err := advanceSourceBill(ctx, tx, req, bill, actor.UserID)
			if err != nil {
				return err
			}
			if b.Status != model.PaymentBillStatusCompleted {
				allPaid = false
			}
			next = append(next, b)
		}
		records, xerr := sourceBillRecords(ctx, tx, next)
		if xerr != nil {
			return xerr
		}
		result = &av.CorrectSourceBillsResponse{Bills: records, AllPaid: allPaid}
		data, xerr := json.Marshal(result)
		if xerr != nil {
			return xerr
		}
		_, xerr = tx.ExecContext(
			ctx,
			`INSERT INTO payment.admin_source_operation(operation_id,actor_id,input_hash,response)VALUES(?,?,?,?::jsonb)`,
			req.OperationId,
			actor.UserID,
			hash,
			string(data),
		)
		return xerr
	})
	return result, err
}

func requestedRound(round []int64) int64 {
	if len(round) == 0 || round[0] == 0 {
		return 1
	}
	return round[0]
}

func validateBillRound(bill *model.PaymentBill, round []int64) error {
	current := bill.LifecycleRound
	if current == 0 {
		current = 1
	}
	if bill.SupersededAt != nil || bill.AdminOperationID != "" || requestedRound(round) != current {
		return adminflow.Failure(connect.CodeAborted, "账单已重新处理，请刷新后核对当前交易")
	}
	return nil
}

func validateSourceRound(ctx context.Context, source string, id, round int64) error {
	table, err := sourceTable(source)
	if err != nil {
		return err
	}
	var row struct {
		LifecycleRound   int64
		AdminOperationID string
	}
	err = postgres.DB.NewRaw(`SELECT lifecycle_round,admin_operation_id FROM ? WHERE id=?`, bun.Ident(table), id).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) && round == 1 {
		return nil
	}
	if err != nil {
		return err
	}
	if row.LifecycleRound != round || row.AdminOperationID != "" {
		return adminflow.Failure(connect.CodeAborted, "业务已进入其他处理轮次")
	}
	return nil
}

func validateSourceCorrection(req *av.CorrectSourceBillsRequest) (int64, error) {
	if req == nil {
		return 0, adminflow.Failure(connect.CodeInvalidArgument, "缺少纠错参数")
	}
	if _, err := sourceTable(req.SourceType); err != nil {
		return 0, err
	}
	sourceID, err := admin.ParseID(req.SourceId)
	if err != nil {
		return 0, err
	}
	if req.ExpectedRound < 1 || req.TargetRound != req.ExpectedRound+1 || len(req.OperationId) < 10 ||
		len(req.OperationId) > 128 ||
		len(req.ExpectedBills) > 1000 ||
		len(req.RestorationBills) > 1000 {
		return 0, adminflow.Failure(connect.CodeInvalidArgument, "纠错轮次或操作编号无效")
	}
	switch req.Action {
	case "cancel", "reopen", "reverse_completion":
	default:
		return 0, adminflow.Failure(connect.CodeInvalidArgument, "不支持该账单联动动作")
	}
	switch req.FundsHandling {
	case "no_payment_received", "retain", "refunded", "confirmation_error":
	default:
		return 0, adminflow.Failure(connect.CodeInvalidArgument, "请选择款项处理方式")
	}
	if err = admin.ValidateReason(req.Reason, req.Evidence); err != nil {
		return 0, err
	}
	if strings.TrimSpace(req.Evidence) == "" {
		return 0, adminflow.Failure(connect.CodeInvalidArgument, "请登记核实依据")
	}
	return sourceID, nil
}

func validateSourceReservation(
	ctx context.Context,
	tx bun.Tx,
	req *av.CorrectSourceBillsRequest,
	sourceID int64,
) error {
	// Source locks cannot cross an RPC boundary; the committed reservation marker
	// fences ordinary writes while this idempotent payment transaction runs.
	table, err := sourceTable(req.SourceType)
	if err != nil {
		return err
	}
	var source struct {
		LifecycleRound   int64
		AdminOperationID string
	}
	if err = tx.NewRaw(`SELECT lifecycle_round,admin_operation_id FROM ? WHERE id=?`, bun.Ident(table), sourceID).
		Scan(ctx, &source); err != nil {
		return err
	}
	if source.LifecycleRound != req.ExpectedRound ||
		source.AdminOperationID != strings.TrimSuffix(req.OperationId, ":payment") {
		return adminflow.Failure(connect.CodeAborted, "业务源轮次或纠错占用已变化")
	}
	return nil
}

func restoreHistoricalBills(
	ctx context.Context,
	tx bun.Tx,
	req *av.CorrectSourceBillsRequest,
	sourceID, actorID int64,
) ([]*model.PaymentBill, error) {
	var bills []*model.PaymentBill
	if len(req.RestorationBills) > 0 {
		used := map[int64]bool{}
		for _, restore := range req.RestorationBills {
			payer, perr := admin.ParseID(restore.PayerId)
			if perr != nil {
				return nil, perr
			}
			payee, perr := admin.ParseID(restore.PayeeId)
			if perr != nil {
				return nil, perr
			}
			if err := validateRestorationBill(restore, payer, payee, used); err != nil {
				return nil, err
			}
			used[payer] = true
			number, perr := newPaymentBillNo()
			if perr != nil {
				return nil, perr
			}
			source := req.SourceType
			// validateRestorationBill bounds this amount to [0, MaxInt32].
			amount := int32(restore.AmountCents) // #nosec G115
			b := &model.PaymentBill{
				BillNo:         number,
				PayerID:        payer,
				PayeeID:        payee,
				AmountCents:    amount,
				Status:         model.PaymentBillStatus(restore.Status),
				SourceType:     &source,
				SourceID:       &sourceID,
				LifecycleRound: req.ExpectedRound,
				VerifyCode:     model.GenerateVerifyCode(),
			}
			if restore.Status == "completed" {
				now := time.Now()
				b.CompletedAt = &now
			}
			if _, perr = tx.NewInsert().Model(b).Returning("*").Exec(ctx); perr != nil {
				return nil, perr
			}
			if perr = admin.WriteAudit(
				ctx,
				tx,
				admin.Entry{
					Domain:     "payment",
					ActorID:    actorID,
					Action:     "restore_bill_history",
					TargetType: "bill",
					TargetID:   b.ID,
					RequestID:  req.OperationId,
					Reason:     req.Reason,
					Evidence:   req.Evidence,
					Before:     nil,
					After:      b,
				},
			); perr != nil {
				return nil, perr
			}
			bills = append(bills, b)
		}
	}
	return bills, nil
}

func advanceSourceBill(
	ctx context.Context,
	tx bun.Tx,
	req *av.CorrectSourceBillsRequest,
	bill *model.PaymentBill,
	actorID int64,
) (*model.PaymentBill, error) {
	if bill.AdminOperationID != "" {
		return nil, adminflow.Failure(connect.CodeAborted, "关联账单正在被其他纠错处理")
	}
	if bill.SupersededAt != nil {
		return nil, adminflow.Failure(connect.CodeAborted, "当前账单已进入其他处理轮次")
	}
	if err := validateSubmittedFunds(req, bill); err != nil {
		return nil, err
	}
	funding, refunded, reversed, xerr := billFundingState(ctx, tx, bill)
	if xerr != nil {
		return nil, xerr
	}
	if xerr = applySourceFunds(ctx, tx, req, bill, actorID, funding, refunded, reversed); xerr != nil {
		return nil, xerr
	}
	nextStatus := nextSourceBillStatus(req, bill, refunded)
	number, xerr := newPaymentBillNo()
	if xerr != nil {
		return nil, xerr
	}
	now := time.Now()
	if _, xerr = tx.NewUpdate().
		Model((*model.PaymentBill)(nil)).
		Set("superseded_at=?", now).
		Set("updated_at=?", now).
		Where("id=?", bill.ID).
		Exec(ctx); xerr != nil {
		return nil, xerr
	}
	b := &model.PaymentBill{
		BillNo:         number,
		PayerID:        bill.PayerID,
		PayeeID:        bill.PayeeID,
		SourceType:     bill.SourceType,
		SourceID:       bill.SourceID,
		AmountCents:    bill.AmountCents,
		VerifyCode:     model.GenerateVerifyCode(),
		Status:         nextStatus,
		LifecycleRound: req.TargetRound,
	}
	if nextStatus == model.PaymentBillStatusCompleted {
		b.FundingBillID = &funding
		b.CompletedAt = bill.CompletedAt
		b.Channel = bill.Channel
		b.SerialNumber = bill.SerialNumber
	}
	if nextStatus == model.PaymentBillStatusClosed {
		b.ClosedAt = &now
		if bill.Status == model.PaymentBillStatusCompleted || bill.FundingBillID != nil {
			b.FundingBillID = &funding
		}
	}
	if _, xerr = tx.NewInsert().Model(b).Returning("*").Exec(ctx); xerr != nil {
		return nil, xerr
	}
	if xerr = admin.WriteAudit(
		ctx,
		tx,
		admin.Entry{
			Domain:     "payment",
			ActorID:    actorID,
			Action:     req.Action,
			TargetType: "bill",
			TargetID:   bill.ID,
			RequestID:  req.OperationId,
			Reason:     req.Reason,
			Evidence:   req.Evidence,
			Before:     bill,
			After:      b,
		},
	); xerr != nil {
		return nil, xerr
	}
	return b, nil
}

func applySourceFunds(
	ctx context.Context,
	tx bun.Tx,
	req *av.CorrectSourceBillsRequest,
	bill *model.PaymentBill,
	actorID, funding int64,
	refunded, reversed bool,
) error {
	var xerr error
	if bill.Status == model.PaymentBillStatusCompleted {
		if req.Action == "cancel" && req.FundsHandling != "refunded" && req.FundsHandling != "confirmation_error" {
			return adminflow.Failure(connect.CodeFailedPrecondition, "取消已收款交易前须核实全额退款")
		}
		if req.FundsHandling == "no_payment_received" {
			return adminflow.Failure(connect.CodeFailedPrecondition, "已确认收款不能按未收款处理")
		}
		if req.FundsHandling == "retain" && (refunded || reversed) {
			return adminflow.Failure(connect.CodeFailedPrecondition, "已登记退款的款项不能结转为已付款")
		}
		if req.FundsHandling == "confirmation_error" {
			if refunded {
				return adminflow.Failure(connect.CodeFailedPrecondition, "已退款款项不能登记为从未收到")
			}
			if _, xerr = tx.ExecContext(
				ctx,
				`INSERT INTO payment.admin_confirmation_reversal(bill_id,actor_id,operation_id,reason,evidence)VALUES(?,?,?,?,?) ON CONFLICT(bill_id) DO NOTHING`,
				funding,
				actorID,
				req.OperationId,
				req.Reason,
				req.Evidence,
			); xerr != nil {
				return xerr
			}
		}
		if req.FundsHandling == "refunded" {
			if xerr = registerFullRefund(
				ctx,
				tx,
				bill,
				actorID,
				req.OperationId,
				req.Reason,
				req.Evidence,
			); xerr != nil {
				return xerr
			}
		}
	}
	return nil
}

func sameSourceBillSnapshot(want *av.SourceBill, got *model.PaymentBill) bool {
	return want.UpdatedAt != nil && want.UpdatedAt.IsValid() && want.UpdatedAt.AsTime().Equal(got.UpdatedAt) &&
		want.Status == string(got.Status) &&
		want.LifecycleRound == got.LifecycleRound &&
		want.AmountCents == int64(got.AmountCents) &&
		want.PayerId == fmt.Sprint(got.PayerID) &&
		want.PayeeId == fmt.Sprint(got.PayeeID) &&
		want.Superseded == (got.SupersededAt != nil)
}

func beginSourceCorrection(
	ctx context.Context,
	tx bun.Tx,
	req *av.CorrectSourceBillsRequest,
	hash string,
) (*av.CorrectSourceBillsResponse, bool, error) {
	var xerr error
	if _, xerr = tx.ExecContext(
		ctx,
		`SELECT set_config('sast.admin_operation_id',?,true)`,
		strings.TrimSuffix(req.OperationId, ":payment"),
	); xerr != nil {
		return nil, false, xerr
	}
	if _, xerr = tx.ExecContext(
		ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended(?,23))`,
		req.OperationId,
	); xerr != nil {
		return nil, false, xerr
	}
	var stored struct {
		InputHash string
		Response  json.RawMessage
	}
	xerr = tx.NewRaw(`SELECT input_hash,response FROM payment.admin_source_operation WHERE operation_id=?`, req.OperationId).
		Scan(ctx, &stored)
	if xerr == nil {
		if stored.InputHash != hash {
			return nil, false, adminflow.Failure(connect.CodeAlreadyExists, "纠错编号与原请求内容不一致")
		}
		var result av.CorrectSourceBillsResponse
		err := json.Unmarshal(stored.Response, &result)
		return &result, true, err
	}
	if !errors.Is(xerr, sql.ErrNoRows) {
		return nil, false, xerr
	}
	return nil, false, nil
}

func validateRestorationBill(restore *av.SourceBill, payer, payee int64, used map[int64]bool) error {
	if payer == payee || used[payer] || restore.AmountCents < 0 || restore.AmountCents > 2147483647 ||
		restore.Id != "" ||
		(restore.Status != "unpaid" && restore.Status != "completed") {
		return adminflow.Failure(connect.CodeInvalidArgument, "补录账单的付款人、金额或状态无效")
	}
	return nil
}

func billFundingState(ctx context.Context, db bun.IDB, bill *model.PaymentBill) (int64, bool, bool, error) {
	funding := bill.ID
	if bill.FundingBillID != nil {
		funding = *bill.FundingBillID
	}
	refunded, xerr := db.NewSelect().TableExpr("payment.admin_refund").Where("bill_id=?", funding).Exists(ctx)
	if xerr != nil {
		return 0, false, false, xerr
	}
	reversed, xerr := db.NewSelect().
		TableExpr("payment.admin_confirmation_reversal").
		Where("bill_id=?", funding).
		Exists(ctx)
	if xerr != nil {
		return 0, false, false, xerr
	}
	return funding, refunded, reversed, nil
}

func loadSourceCorrectionBills(
	ctx context.Context,
	tx bun.Tx,
	req *av.CorrectSourceBillsRequest,
	sourceID, actorID int64,
) ([]*model.PaymentBill, error) {
	var xerr error
	var bills []*model.PaymentBill
	if xerr = tx.NewSelect().
		Model(&bills).
		Where("source_type=? AND source_id=? AND lifecycle_round=?", req.SourceType, sourceID, req.ExpectedRound).
		OrderExpr("id ASC").
		For("UPDATE").
		Scan(ctx); xerr != nil {
		return nil, xerr
	}
	if xerr = validateSourceSnapshot(bills, req.ExpectedBills); xerr != nil {
		return nil, xerr
	}
	if len(bills) > 0 && len(req.RestorationBills) > 0 {
		return nil, adminflow.Failure(connect.CodeInvalidArgument, "已有账单不能重复补录")
	}
	if len(req.RestorationBills) > 0 {
		bills, xerr = restoreHistoricalBills(ctx, tx, req, sourceID, actorID)
		if xerr != nil {
			return nil, xerr
		}
	}
	return bills, nil
}

func nextSourceBillStatus(
	req *av.CorrectSourceBillsRequest,
	bill *model.PaymentBill,
	refunded bool,
) model.PaymentBillStatus {
	nextStatus := model.PaymentBillStatusUnpaid
	if req.Action == "cancel" {
		nextStatus = model.PaymentBillStatusClosed
	} else if req.FundsHandling == "retain" && bill.Status == model.PaymentBillStatusCompleted && !refunded {
		nextStatus = model.PaymentBillStatusCompleted
	}
	return nextStatus
}

func validateSubmittedFunds(req *av.CorrectSourceBillsRequest, bill *model.PaymentBill) error {
	if bill.Status == model.PaymentBillStatusSubmitted && req.FundsHandling != "no_payment_received" &&
		!req.SubmittedUnreceivedConfirmed {
		return adminflow.Failure(connect.CodeFailedPrecondition, "已申报付款需先核查到账，或明确登记未收款依据")
	}
	return nil
}
