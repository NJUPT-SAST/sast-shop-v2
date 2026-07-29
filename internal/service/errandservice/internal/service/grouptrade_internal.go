package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/repository"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
)

// 错误定义
var (
	ErrTaskNotFound               = errors.New("errand task not found")
	ErrTaskNotInCollectingPayment = errors.New("errand task is not in collecting payment status")
	ErrAssignmentNotFound         = errors.New("errand task payment assignment not found")
)

// 单笔账单确认收款，流转推这个买家的demand_item和demand状态。
func OnErrandTaskPaymentConfirmed(ctx context.Context, taskID, payerID int64) error {
	return repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		task, err := repository.GetTaskByID(ctx, tx, taskID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrTaskNotFound
			}
			log.Error().Err(err).Int64("task_id", taskID).Msg("get task failed")
			return newErrandInternalError(fmt.Sprintf("failed to get errand task, task_id=%d", taskID))
		}

		// 已经completed的不变
		if task.Status == model.ErrandTaskStatusCompleted {
			log.Debug().Int64("task_id", taskID).Msg("task already completed, skip OnPaymentConfirmed")
			return nil
		}
		// 其他状态拒绝流转
		if task.Status != model.ErrandTaskStatusCollectingPayment {
			log.Warn().
				Int64("task_id", taskID).
				Str("status", string(task.Status)).
				Msg("task not in collecting_payment, refuse to advance")
			return ErrTaskNotInCollectingPayment
		}

		// 根据taskID, payerID获取assignments
		assignments, err := repository.GetAssignmentsByTaskAndPurchaser(ctx, tx, taskID, payerID)
		if err != nil {
			log.Error().Err(err).
				Int64("task_id", taskID).
				Int64("payer_id", payerID).
				Msg("query assignments failed")
			return newErrandInternalError(
				fmt.Sprintf(
					"failed to query errand task payment assignments, task_id=%d, payer_id=%d",
					taskID,
					payerID,
				),
			)
		}
		if len(assignments) == 0 {
			return ErrAssignmentNotFound
		}

		// 收集要推进的 demand_item 和它们所属的 demand
		itemIDs := make([]int64, 0, len(assignments))
		for _, assignment := range assignments {
			itemIDs = append(itemIDs, assignment.DemandItemID)
		}

		now := time.Now().UTC()
		// 将所有 demand_item 状态流转至完成
		if _, err := repository.MarkDemandItemsCompletedByIDs(ctx, tx, itemIDs, now); err != nil {
			log.Error().Err(err).Msg("mark demand items completed failed")
			return newErrandInternalError(
				fmt.Sprintf("failed to mark errand demand items completed, task_id=%d, payer_id=%d", taskID, payerID),
			)
		}

		// 将所有 assignment 涉及到的所有 demand_item 都完成的demand状态流转到完成
		if _, err := repository.MarkDemandsCompletedIfAllItemsDoneByItemIDs(ctx, tx, itemIDs, now); err != nil {
			log.Error().Err(err).Msg("mark demands completed failed")
			return newErrandInternalError(
				fmt.Sprintf("failed to mark errand demands completed, task_id=%d, payer_id=%d", taskID, payerID),
			)
		}

		return nil
	})
}

// 流转整个 task 到 completed。
func OnErrandTaskAllPaymentsConfirmed(ctx context.Context, taskID int64) error {
	return repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		task, err := repository.GetTaskByID(ctx, tx, taskID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrTaskNotFound
			}
			log.Error().Err(err).Int64("task_id", taskID).Msg("get task failed")
			return newErrandInternalError(fmt.Sprintf("failed to get errand task, task_id=%d", taskID))
		}

		if task.Status == model.ErrandTaskStatusCompleted {
			return nil
		}
		if task.Status != model.ErrandTaskStatusCollectingPayment {
			log.Warn().
				Int64("task_id", taskID).
				Str("status", string(task.Status)).
				Msg("task not in collecting_payment, refuse to complete")
			return ErrTaskNotInCollectingPayment
		}

		if _, err := repository.MarkTaskCompleted(ctx, tx, taskID, time.Now().UTC()); err != nil {
			log.Error().Err(err).Int64("task_id", taskID).Msg("mark task completed failed")
			return newErrandInternalError(fmt.Sprintf("failed to mark errand task completed, task_id=%d", taskID))
		}
		return nil
	})
}
