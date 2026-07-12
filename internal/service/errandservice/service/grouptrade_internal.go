package service

import (
	"context"
	"database/sql"
	"errors"

	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/repository"
	"github.com/rs/zerolog/log"
)

// 错误定义
var (
	ErrTaskNotFound       = errors.New("errand task not found")
	ErrAssignmentNotFound = errors.New("errand task assignment not found")
)

// 单笔账单确认收款，流转推这个买家的demand_item和demand状态。
func OnErrandTaskPaymentConfirmed(ctx context.Context, taskID, payerID int64) error {
	task, err := repository.GetTaskByID(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTaskNotFound
		}
		log.Error().Err(err).Int64("task_id", taskID).Msg("get task failed")
		return errandInternalError()
	}

	//已经completed的不变
	if task.Status == model.ErrandTaskStatusCompleted {
		log.Debug().Int64("task_id", taskID).Msg("task already completed, skip OnPaymentConfirmed")
		return nil
	}
	//其他状态拒绝流转
	if task.Status != model.ErrandTaskStatusCollectingPayment {
		log.Warn().
			Int64("task_id", taskID).
			Str("status", string(task.Status)).
			Msg("task not in collecting_payment, refuse to advance")
		return errandInternalError()
	}

	//根据taskID, payerID获取assignments
	assignments, err := repository.GetAssignmentsByTaskAndPurchaser(ctx, taskID, payerID)
	if err != nil {
		log.Error().Err(err).
			Int64("task_id", taskID).
			Int64("payer_id", payerID).
			Msg("query assignments failed")
		return errandInternalError()
	}
	if len(assignments) == 0 {
		return ErrAssignmentNotFound
	}

	// 收集要推进的 demand_item 和它们所属的 demand
	itemIDs := make([]int64, 0, len(assignments))
	demandIDSet := make(map[int64]struct{}, len(assignments))
	for _, a := range assignments {
		itemIDs = append(itemIDs, a.DemandItemID)
	}

	//将所有 demand_item 状态流转至完成
	if _, err := repository.MarkDemandItemsCompletedByIDs(ctx, itemIDs); err != nil {
		log.Error().Err(err).Msg("mark demand items completed failed")
		return errandInternalError()
	}

	// 根据 demand_item 拿 demand_id（assignment 里没有这一列）
	items, err := repository.GetDemandItemsByIDs(ctx, itemIDs)
	if err != nil {
		log.Error().Err(err).Msg("reload demand items failed")
		return errandInternalError()
	}
	for _, it := range items {
		demandIDSet[it.ErrandDemandID] = struct{}{}
	}

	//将所有 assignment 涉及到的所有 demand_item 都完成的demand状态流转到完成
	for demandID := range demandIDSet {
		if _, err := repository.MarkDemandCompletedIfAllItemsDone(ctx, demandID); err != nil {
			log.Error().Err(err).Int64("demand_id", demandID).
				Msg("mark demand completed failed")
			return errandInternalError()
		}
	}

	return nil
}

// 流转整个 task 到 completed。
func OnErrandTaskAllPaymentsConfirmed(ctx context.Context, taskID int64) error {
	task, err := repository.GetTaskByID(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTaskNotFound
		}
		log.Error().Err(err).Int64("task_id", taskID).Msg("get task failed")
		return errandInternalError()
	}

	if task.Status == model.ErrandTaskStatusCompleted {
		return nil
	}
	if task.Status != model.ErrandTaskStatusCollectingPayment {
		log.Warn().
			Int64("task_id", taskID).
			Str("status", string(task.Status)).
			Msg("task not in collecting_payment, refuse to complete")
		return errandInternalError()
	}

	if _, err := repository.MarkTaskCompleted(ctx, taskID); err != nil {
		log.Error().Err(err).Int64("task_id", taskID).Msg("mark task completed failed")
		return errandInternalError()
	}
	return nil
}

func errandInternalError() error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_ErrandError{
		ErrandError: &errandv1.ErrandError{
			Code: errandv1.ErrandErrorCode_ERRAND_ERROR_CODE_INTERNAL_ERROR,
		},
	}, "")
}
