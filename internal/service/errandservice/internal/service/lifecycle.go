package service

import (
	"context"
	"database/sql"
	"errors"

	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"

	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/repository"
	"github.com/uptrace/bun"
)

func checkErrandLifecycle(current, requested int64, operation string) error {
	if requested < 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("处理轮次无效"))
	}
	if operation != "" {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("交易正在由管理员处理，请稍后刷新"))
	}
	if max(current, 1) != max(requested, 1) {
		return connect.NewError(connect.CodeAborted, errors.New("交易已进入新的处理轮次，请刷新后重试"))
	}
	return nil
}

func validateCreatedErrandBill(bill *paymentv1.Bill, taskID, round int64, draft taskPaymentBillDraft) error {
	if bill.GetSourceType() != "errand_task" || bill.GetSourceId() != taskID ||
		max(bill.LifecycleRound, 1) != max(round, 1) ||
		bill.AmountCents != draft.AmountCents ||
		bill.Status == paymentv1.BillStatus_BILL_STATUS_CLOSED {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("返回账单与当前任务金额或轮次不一致，请核实账单"))
	}
	return nil
}

func validateErrandBoundBills(ctx context.Context, tx bun.Tx, taskID int64, bills []boundBillDraft) error {
	rows, err := repository.ListTaskPaymentBillAssignments(ctx, tx, taskID)
	if err != nil {
		return err
	}
	drafts, err := buildTaskPaymentBillDrafts(rows)
	if err != nil {
		return err
	}
	expected := map[int64]taskPaymentBillDraft{}
	for _, draft := range drafts {
		if draft.PayerID != draft.PayeeID {
			expected[draft.PayerID] = draft
		}
	}
	if len(expected) != len(bills) {
		return connect.NewError(connect.CodeAborted, ErrConcurrencyConflict)
	}
	for _, bill := range bills {
		draft, ok := expected[bill.PayerID]
		if !ok || draft.AmountCents != bill.AmountCents || draft.PayeeID != bill.PayeeID {
			return connect.NewError(connect.CodeAborted, ErrConcurrencyConflict)
		}
		delete(expected, bill.PayerID)
	}
	return nil
}

func preflightErrandCollection(
	ctx context.Context,
	captainID int64,
	req *errandv1.TransitionToCollectingPaymentRequest,
) error {
	return repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if err := guardErrandTask(ctx, tx, req.ErrandTaskId, captainID, req.LifecycleRound); err != nil {
			return err
		}
		task, err := loadDistributingTaskForCollectingPayment(
			ctx,
			tx,
			captainID,
			req.ErrandTaskId,
			req.UpdatedAt.AsTime(),
		)
		if err != nil {
			return err
		}
		return ensureTaskDistributionCompleted(ctx, tx, task.TaskID)
	})
}

func lockCurrentErrandTask(ctx context.Context, db bun.IDB, id, captainID, round int64) (*model.ErrandTask, error) {
	var task model.ErrandTask
	if err := db.NewSelect().Model(&task).Where("id = ?", id).For("UPDATE").Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, ErrTaskNotFound)
		}
		return nil, err
	}
	if captainID != 0 && task.CaptainID != captainID {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("仅任务负责人可以操作"))
	}
	if err := checkErrandLifecycle(task.LifecycleRound, round, task.AdminOperationID); err != nil {
		return nil, err
	}
	return &task, nil
}

func guardErrandTask(ctx context.Context, tx bun.Tx, id, captainID, round int64) error {
	_, err := lockCurrentErrandTask(ctx, tx, id, captainID, round)
	return err
}

func guardErrandAssignment(ctx context.Context, tx bun.Tx, assignmentID, captainID, round int64) error {
	var taskID int64
	if err := tx.NewSelect().
		Model((*model.ErrandTaskAssignment)(nil)).
		Column("task_id").
		Where("id=?", assignmentID).
		Scan(ctx, &taskID); err != nil {
		return err
	}
	return guardErrandTask(ctx, tx, taskID, captainID, round)
}
