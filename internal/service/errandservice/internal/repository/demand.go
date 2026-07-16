package repository

import (
	"context"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
)

// CreateDemand 插入一条 errand_demand，返回自增 ID。
// service 层负责填充 RequesterID、StoreID、Deadline。
func CreateDemand(ctx context.Context, demand *model.ErrandDemand) (int64, error) {
	demand.Status = model.ErrandDemandStatusOpen
	demand.CreatedAt = time.Now()
	demand.UpdatedAt = time.Now()

	_, err := postgres.DB.NewInsert().
		Model(demand).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	return demand.ID, nil
}

// BatchCreateDemandItems 批量插入 errand_demand_item。
// service 层负责填充 ErrandDemandID、RequesterID、StoreID、ProductTemplateID、
// Quantity、ServiceFeePerUnitCents、EstimatedUnitPriceCents。
func BatchCreateDemandItems(ctx context.Context, items []*model.ErrandDemandItem) error {
	if len(items) == 0 {
		return nil
	}

	now := time.Now()
	for _, item := range items {
		item.Status = model.ErrandDemandItemStatusOpen
		item.CreatedAt = now
		item.UpdatedAt = now
	}

	_, err := postgres.DB.NewInsert().
		Model(&items).
		Exec(ctx)
	return err
}

// DemandListAggregation 按店铺聚合的需求统计信息。
// GetDemandList 用它返回每个店铺的汇总数据。
type DemandListAggregation struct {
	StoreID                   int64     `bun:"store_id"`
	TotalOriginUnitPriceCents int32     `bun:"total_origin_unit_price_cents"`
	TotalServiceFeeCents      int32     `bun:"total_service_fee_cents"`
	LatestUpdatedAt           time.Time `bun:"latest_updated_at"`
}

// GetDemandListByStore 按店铺聚合查询 open 状态的需求统计。
// 返回每个店铺的总预估价格、总跑腿费、最后更新时间。
// 按 latest_updated_at 倒序排列（最新的需求排前面）。
func GetDemandListByStore(
	ctx context.Context,
	page, pageSize int32,
	storeName string,
) ([]*DemandListAggregation, int, error) {
	query := postgres.DB.NewSelect().
		ColumnExpr("store_id").
		ColumnExpr("SUM(estimated_unit_price_cents * quantity) AS total_origin_unit_price_cents").
		ColumnExpr("SUM(service_fee_per_unit_cents * quantity) AS total_service_fee_cents").
		ColumnExpr("MAX(updated_at) AS latest_updated_at").
		TableExpr("errand.errand_demand_item").
		Where("status = ?", model.ErrandDemandItemStatusOpen).
		Group("store_id").
		Order("latest_updated_at DESC")

	// 如果有店铺名搜索，需要 JOIN catalog_store
	// 但 catalog 在另一个服务，这里先跳过，service 层过滤
	// 或者在这里 JOIN catalog.catalog_store，看你的设计
	// 为简化，这里假设 service 层调 catalog 服务后再过滤

	// 先查总数
	totalCount, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	// 分页查询
	offset := (page - 1) * pageSize
	var results []*DemandListAggregation
	err = query.
		Limit(int(pageSize)).
		Offset(int(offset)).
		Scan(ctx, &results)

	return results, totalCount, err
}

// GetDistinctRequestersByStore 查询某店铺下参与的买家 ID（去重）。
// 用于获取买家头像，通常只取前 N 个。
func GetDistinctRequestersByStore(
	ctx context.Context,
	storeID int64,
	limit int,
) ([]int64, error) {
	var requesterIDs []int64
	err := postgres.DB.NewSelect().
		ColumnExpr("DISTINCT requester_id").
		TableExpr("errand.errand_demand_item").
		Where("status = ?", model.ErrandDemandItemStatusOpen).
		Where("store_id = ?", storeID).
		Limit(limit).
		Scan(ctx, &requesterIDs)
	return requesterIDs, err
}

// GetOpenDemandItemsByStore 查询某店铺下所有 open 状态的 demand_item。
// 按 product_template_id 和 updated_at 排序，方便 service 层分组聚合。
func GetOpenDemandItemsByStore(
	ctx context.Context,
	storeID int64,
) ([]*model.ErrandDemandItem, error) {
	var items []*model.ErrandDemandItem
	err := postgres.DB.NewSelect().
		Model(&items).
		Where("store_id = ?", storeID).
		Where("status = ?", model.ErrandDemandItemStatusOpen).
		Order("product_template_id ASC", "updated_at DESC").
		Scan(ctx)
	return items, err
}

// GetDemandByID 根据 ID 查询单条 errand_demand。
// service 层校验用，或者拼接响应时需要 demand 信息。
func GetDemandByID(ctx context.Context, demandID int64) (*model.ErrandDemand, error) {
	var demand model.ErrandDemand
	err := postgres.DB.NewSelect().
		Model(&demand).
		Where("id = ?", demandID).
		Scan(ctx)
	return &demand, err
}
