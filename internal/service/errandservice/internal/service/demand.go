package service

import (
	"context"
	"errors"
	"strings"
	"time"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/client"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/repository"
	"github.com/rs/zerolog/log"
)

// 业务哨兵错误，handler 层用 errors.Is 来分辨错误类型。
var (
	ErrProductInvalid   = errors.New("product invalid or version mismatch")
	ErrDuplicateProduct = errors.New("duplicate product in demand items")
	ErrInvalidDeadline  = errors.New("deadline must be in the future")
	ErrEmptyDemandItems = errors.New("demand items cannot be empty")
	ErrInvalidQuantity  = errors.New("quantity must be greater than 0")
	ErrInternal         = errors.New("internal error")
)

// DemandItemDraft 创建需求时的商品草稿，对应 proto 的 DemandItem。
type DemandItemDraft struct {
	ProductTemplateID      int64
	Quantity               int32
	ServiceFeePerUnitCents int32
	UpdatedAt              time.Time // 商品模板版本号，用于乐观锁校验
}

// CreateErrandDemand — 买家创建跑腿需求
// CreateErrandDemand 买家创建跑腿需求订单。
// 返回新创建的 errand_demand ID。
func CreateErrandDemand(
	ctx context.Context,
	requesterID int64,
	storeID int64,
	deadline time.Time,
	items []DemandItemDraft,
) (int64, error) {
	// 1. 参数校验
	if len(items) == 0 {
		return 0, ErrEmptyDemandItems
	}
	if deadline.Before(time.Now()) {
		return 0, ErrInvalidDeadline
	}

	// 检查重复商品（同一个 product_template_id 不能出现两次）
	productSet := make(map[int64]struct{})
	for _, item := range items {
		if item.Quantity <= 0 {
			return 0, ErrInvalidQuantity
		}
		if _, exists := productSet[item.ProductTemplateID]; exists {
			return 0, ErrDuplicateProduct
		}
		productSet[item.ProductTemplateID] = struct{}{}
	}

	// 2. 逐个调用 catalog 服务校验商品：存在性、归属、版本
	productMap, err := validateProducts(ctx, storeID, items)
	if err != nil {
		return 0, err
	}

	// 3. 插入 errand_demand 主记录
	demand := &model.ErrandDemand{
		RequesterID: requesterID,
		StoreID:     storeID,
		Deadline:    deadline,
	}
	demandID, err := repository.CreateDemand(ctx, demand)
	if err != nil {
		log.Error().Err(err).Msg("create demand failed")
		return 0, ErrInternal
	}

	// 4. 批量插入 errand_demand_item 明细
	demandItems := make([]*model.ErrandDemandItem, 0, len(items))
	for _, item := range items {
		demandItems = append(demandItems, &model.ErrandDemandItem{
			ErrandDemandID:          demandID,
			RequesterID:             requesterID,
			StoreID:                 storeID,
			ProductTemplateID:       item.ProductTemplateID,
			Quantity:                item.Quantity,
			ServiceFeePerUnitCents:  item.ServiceFeePerUnitCents,
			EstimatedUnitPriceCents: productMap[item.ProductTemplateID].PriceCents,
		})
	}

	if err := repository.BatchCreateDemandItems(ctx, demandItems); err != nil {
		log.Error().Err(err).Msg("batch create demand items failed")
		return 0, ErrInternal
	}

	return demandID, nil
}

// validateProducts 逐个校验商品：存在、归属店铺、版本号匹配。
// 返回 productID → ProductTemplate 的映射。
func validateProducts(
	ctx context.Context,
	storeID int64,
	items []DemandItemDraft,
) (map[int64]*catalogv1.ProductTemplate, error) {
	productMap := make(map[int64]*catalogv1.ProductTemplate)
	for _, item := range items {
		p, err := client.GetProductTemplate(ctx, item.ProductTemplateID)
		if err != nil {
			log.Error().Err(err).Int64("product_id", item.ProductTemplateID).Msg("get product template failed")
			return nil, ErrProductInvalid
		}
		// 校验商品是否属于当前店铺
		if p.StoreId != storeID {
			log.Warn().Int64("product_id", p.Id).Int64("store_id", storeID).
				Msg("product does not belong to this store")
			return nil, ErrProductInvalid
		}
		// 校验版本号（防止买家看到的是已过期的商品信息）
		if !p.UpdatedAt.AsTime().Equal(item.UpdatedAt) {
			log.Warn().Int64("product_id", item.ProductTemplateID).
				Msg("product version mismatch")
			return nil, ErrProductInvalid
		}
		productMap[p.Id] = p
	}
	return productMap, nil
}

// GetDemandList — 队长查看需求大厅列表
// DemandByStoreResult 按店铺聚合的需求大厅列表项。
type DemandByStoreResult struct {
	StoreID                   int64
	StoreName                 string
	ParticipantAvatars        []string
	TotalOriginUnitPriceCents int32
	TotalServiceFeeCents      int32
	UpdatedAt                 time.Time
}

// GetDemandList 队长查看未接单跑腿需求大厅。
// 按店铺聚合，返回每个店铺的总价、跑腿费、参与买家头像。
func GetDemandList(
	ctx context.Context,
	page, pageSize int32,
	storeName string,
) ([]*DemandByStoreResult, int, error) {
	// 1. 参数默认值处理
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	// 2. 查询数据库聚合数据
	aggregations, totalCount, err := repository.GetDemandListByStore(ctx, page, pageSize, storeName)
	if err != nil {
		log.Error().Err(err).Msg("get demand list by store failed")
		return nil, 0, ErrInternal
	}
	if len(aggregations) == 0 {
		return []*DemandByStoreResult{}, 0, nil
	}

	// 3. 逐个查询店铺名称
	storeMap := fetchStoreNames(ctx, aggregations)

	// 4. 按店铺名过滤（前端搜索）
	if storeName != "" {
		aggregations = filterByStoreName(aggregations, storeMap, storeName)
	}

	// 5. 收集各店铺的买家 ID → 批量查头像
	storeRequesters, allRequesterIDs := collectRequesters(ctx, aggregations)
	userMap := fetchUserMap(ctx, allRequesterIDs)

	// 6. 组装最终结果
	results := buildDemandResults(aggregations, storeMap, storeRequesters, userMap)

	return results, totalCount, nil
}

// fetchStoreNames 逐个调用 catalog 服务获取店铺名称。
func fetchStoreNames(ctx context.Context, aggs []*repository.DemandListAggregation) map[int64]string {
	m := make(map[int64]string)
	for _, agg := range aggs {
		if _, ok := m[agg.StoreID]; ok {
			continue // 已查过的店铺跳过
		}
		store, err := client.GetStore(ctx, agg.StoreID)
		if err != nil {
			log.Warn().Err(err).Int64("store_id", agg.StoreID).Msg("get store failed, skip")
			m[agg.StoreID] = ""
			continue
		}
		m[agg.StoreID] = store.Name
	}
	return m
}

// filterByStoreName 按店铺名称模糊匹配过滤聚合结果。
func filterByStoreName(
	aggs []*repository.DemandListAggregation,
	storeMap map[int64]string,
	name string,
) []*repository.DemandListAggregation {
	filtered := make([]*repository.DemandListAggregation, 0)
	for _, agg := range aggs {
		if n, ok := storeMap[agg.StoreID]; ok && strings.Contains(n, name) {
			filtered = append(filtered, agg)
		}
	}
	return filtered
}

// collectRequesters 收集各店铺前 N 个买家 ID，用于后续批量查头像。
func collectRequesters(
	ctx context.Context,
	aggs []*repository.DemandListAggregation,
) (map[int64][]int64, map[int64]struct{}) {
	storeReq := make(map[int64][]int64) // storeID → requesterIDs
	allIDs := make(map[int64]struct{})  // 去重后的所有买家 ID
	for _, agg := range aggs {
		ids, err := repository.GetDistinctRequestersByStore(ctx, agg.StoreID, 3)
		if err != nil {
			log.Warn().Err(err).Int64("store_id", agg.StoreID).Msg("get requesters failed")
			continue
		}
		storeReq[agg.StoreID] = ids
		for _, rid := range ids {
			allIDs[rid] = struct{}{}
		}
	}
	return storeReq, allIDs
}

// fetchUserMap 批量调用 user 服务查询用户信息（头像等）。
func fetchUserMap(ctx context.Context, idSet map[int64]struct{}) map[int64]*userv1.UserInfo {
	m := make(map[int64]*userv1.UserInfo)
	if len(idSet) == 0 {
		return m
	}
	ids := make([]int64, 0, len(idSet))
	for rid := range idSet {
		ids = append(ids, rid)
	}
	users, err := client.GetUsers(ctx, ids)
	if err != nil {
		log.Warn().Err(err).Msg("get users failed, avatars will be empty")
		return m
	}
	for _, u := range users {
		m[u.Id] = u
	}
	return m
}

// buildDemandResults 将数据库聚合结果 + 店铺名 + 头像组装成返回结构。
func buildDemandResults(
	aggs []*repository.DemandListAggregation,
	storeMap map[int64]string,
	storeReq map[int64][]int64,
	userMap map[int64]*userv1.UserInfo,
) []*DemandByStoreResult {
	results := make([]*DemandByStoreResult, 0, len(aggs))
	for _, agg := range aggs {
		avatars := make([]string, 0, 3)
		for _, rid := range storeReq[agg.StoreID] {
			if u, ok := userMap[rid]; ok {
				avatars = append(avatars, u.AvatarUrl)
			}
		}
		results = append(results, &DemandByStoreResult{
			StoreID:                   agg.StoreID,
			StoreName:                 storeMap[agg.StoreID],
			ParticipantAvatars:        avatars,
			TotalOriginUnitPriceCents: agg.TotalOriginUnitPriceCents,
			TotalServiceFeeCents:      agg.TotalServiceFeeCents,
			UpdatedAt:                 agg.LatestUpdatedAt,
		})
	}
	return results
}

// GetDemandDetail — 队长查看某店铺的需求详情
// DemandDetailResult 某个商品的需求详情聚合。
type DemandDetailResult struct {
	ErrandDemandID          int64
	ProductTemplateID       int64
	ProductTitle            string
	ProductDescription      string
	ProductImageURL         string
	EstimatedUnitPriceCents int32
	TotalQuantity           int32
	Requesters              []*DemandDetailRequester
}

// DemandDetailRequester 某个商品下单个买家的需求明细。
type DemandDetailRequester struct {
	RequesterID            int64
	RequesterName          string
	RequesterAvatarURL     string
	Quantity               int32
	ServiceFeePerUnitCents int32
	ErrandDemandItemID     int64
	Deadline               time.Time
	UpdatedAt              time.Time
}

// GetDemandDetail 队长查看某个店铺下所有 open 状态的需求详情。
// 按商品聚合，展示每个商品有哪些买家需要、各需要多少。
func GetDemandDetail(ctx context.Context, storeID int64) ([]*DemandDetailResult, error) {
	// 1. 参数校验
	if storeID <= 0 {
		return nil, errors.New("invalid store_id")
	}

	// 2. 查询该店铺下所有 open 状态的 demand_item
	items, err := repository.GetOpenDemandItemsByStore(ctx, storeID)
	if err != nil {
		log.Error().Err(err).Int64("store_id", storeID).Msg("get open demand items failed")
		return nil, ErrInternal
	}
	if len(items) == 0 {
		return []*DemandDetailResult{}, nil
	}

	// 3. 按商品分组聚合 + 收集买家 ID
	productMap, requesterIDSet := aggregateDemandItems(items)

	// 4. 逐个查询商品信息（标题、描述、图片）补全展示字段
	fetchProductDetails(ctx, productMap)

	// 5. 批量查询买家信息 + 回填到聚合结果
	fillRequesterInfo(ctx, productMap, requesterIDSet)

	// 6. 组装响应
	results := make([]*DemandDetailResult, 0, len(productMap))
	for _, detail := range productMap {
		results = append(results, detail)
	}
	return results, nil
}

// aggregateDemandItems 将 demand_item 列表按 product_template_id 分组，
// 同时收集所有去重后的 requester_id 供后续批量查用户。
func aggregateDemandItems(items []*model.ErrandDemandItem) (map[int64]*DemandDetailResult, map[int64]struct{}) {
	productMap := make(map[int64]*DemandDetailResult)
	requesterIDSet := make(map[int64]struct{})

	for _, item := range items {
		if _, exists := productMap[item.ProductTemplateID]; !exists {
			productMap[item.ProductTemplateID] = &DemandDetailResult{
				ErrandDemandID:          item.ErrandDemandID,
				ProductTemplateID:       item.ProductTemplateID,
				EstimatedUnitPriceCents: item.EstimatedUnitPriceCents,
				Requesters:              []*DemandDetailRequester{},
			}
		}
		detail := productMap[item.ProductTemplateID]
		detail.TotalQuantity += item.Quantity
		detail.Requesters = append(detail.Requesters, &DemandDetailRequester{
			RequesterID:            item.RequesterID,
			Quantity:               item.Quantity,
			ServiceFeePerUnitCents: item.ServiceFeePerUnitCents,
			ErrandDemandItemID:     item.ID,
			UpdatedAt:              item.UpdatedAt,
		})
		requesterIDSet[item.RequesterID] = struct{}{}
	}
	return productMap, requesterIDSet
}

// fetchProductDetails 逐个调用 catalog 服务补全商品展示信息。
func fetchProductDetails(ctx context.Context, productMap map[int64]*DemandDetailResult) {
	for pid, detail := range productMap {
		p, err := client.GetProductTemplate(ctx, pid)
		if err != nil {
			log.Warn().Err(err).Int64("product_id", pid).Msg("get product template failed, skip")
			continue
		}
		detail.ProductTitle = p.Title
		detail.ProductDescription = p.Description
		detail.ProductImageURL = p.MainImageUrl
	}
}

// fillRequesterInfo 批量查询买家信息并回填到聚合结果中。
func fillRequesterInfo(ctx context.Context, productMap map[int64]*DemandDetailResult, idSet map[int64]struct{}) {
	ids := make([]int64, 0, len(idSet))
	for rid := range idSet {
		ids = append(ids, rid)
	}

	userMap := make(map[int64]*userv1.UserInfo)
	if len(ids) > 0 {
		users, err := client.GetUsers(ctx, ids)
		if err != nil {
			log.Warn().Err(err).Msg("get users failed")
		} else {
			for _, u := range users {
				userMap[u.Id] = u
			}
		}
	}

	// 回填买家姓名和头像
	for _, detail := range productMap {
		for _, req := range detail.Requesters {
			if user, ok := userMap[req.RequesterID]; ok {
				req.RequesterName = user.Name
				req.RequesterAvatarURL = user.AvatarUrl
			}
		}
	}
}
