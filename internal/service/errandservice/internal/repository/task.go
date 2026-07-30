package repository

import (
	"context"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/uptrace/bun"
)

// GetAssignmentsByTaskAndPurchaser 根据任务和支付者获取付款对应的assignment，即分配记录
func GetAssignmentsByTaskAndPurchaser(
	ctx context.Context,
	taskID, purchaserID int64,
) ([]*model.ErrandTaskAssignment, error) {
	var assignments []*model.ErrandTaskAssignment
	err := postgres.DB.NewSelect().
		Model(&assignments).
		Where("task_id = ?", taskID).
		Where("purchaser_id = ?", purchaserID).
		Scan(ctx)
	return assignments, err
}

// 获取跑腿任务
func GetTaskByID(ctx context.Context, taskID int64) (*model.ErrandTask, error) {
	var task model.ErrandTask
	err := postgres.DB.NewSelect().Model(&task).Where("id = ?", taskID).Scan(ctx)
	return &task, err
}

// GetDemandItemsByIDs 获取对应客户订单
func GetDemandItemsByIDs(ctx context.Context, ids []int64) ([]*model.ErrandDemandItem, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var items []*model.ErrandDemandItem
	err := postgres.DB.NewSelect().
		Model(&items).
		Where("id IN (?)", bun.List(ids)).
		Scan(ctx)
	return items, err
}

// MarkDemandItemsCompletedByIDs 单个买家购买的所有商品状态流转到完成
func MarkDemandItemsCompletedByIDs(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	now := time.Now()
	res, err := postgres.DB.NewUpdate().
		Model((*model.ErrandDemandItem)(nil)).
		Set("status = ?", model.ErrandDemandItemStatusCompleted).
		Set("updated_at = ?", now).
		Where("id IN (?)", bun.List(ids)).
		Where("status = ?", model.ErrandDemandItemStatusPendingPayment).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// 如果买家任务下的所有商品已流转至完成，则完成买家在任务中的订单的状态流转
func MarkDemandCompletedIfAllItemsDone(ctx context.Context, demandID int64) (int64, error) {
	now := time.Now()
	res, err := postgres.DB.NewUpdate().
		Model((*model.ErrandDemand)(nil)).
		Set("status = ?", model.ErrandDemandStatusCompleted).
		Set("payment_completed_at = ?", now).
		Set("updated_at = ?", now).
		Where("id = ?", demandID).
		Where("status = ?", model.ErrandDemandStatusPendingPayment).
		Where(`NOT EXISTS (
            SELECT 1 FROM errand.errand_demand_item edi
            WHERE edi.errand_demand_id = ? AND edi.status <> ?
        )`, demandID, model.ErrandDemandItemStatusCompleted).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// MarkTaskCompleted 完成团长任务的状态流转
func MarkTaskCompleted(ctx context.Context, taskID int64) (int64, error) {
	now := time.Now()
	res, err := postgres.DB.NewUpdate().
		Model((*model.ErrandTask)(nil)).
		Set("status = ?", model.ErrandTaskStatusCompleted).
		Set("payment_completed_at = ?", now).
		Set("updated_at = ?", now).
		Where("id = ?", taskID).
		Where("status = ?", model.ErrandTaskStatusCollectingPayment).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
