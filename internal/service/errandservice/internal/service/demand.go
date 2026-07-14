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

var (
	ErrProductInvalid   = errors.New("product invalid or version mismatch")
	ErrDuplicateProduct = errors.New("duplicate product in demand items")
	ErrInvalidDeadline  = errors.New("deadline must be in the future")
	ErrEmptyDemandItems = errors.New("demand items cannot be empty")
	ErrInvalidQuantity  = errors.New("quantity must be greater than 0")
	ErrInternal         = errors.New("internal error")
)

type DemandItemDraft struct {
	ProductTemplateID      int64
	Quantity               int32
	ServiceFeePerUnitCents int32
	UpdatedAt              time.Time
}

func CreateErrandDemand(
	ctx context.Context,
	requesterID int64,
	storeID int64,
	deadline time.Time,
	items []DemandItemDraft,
) (int64, error) {
	if len(items) == 0 {
		return 0, ErrEmptyDemandItems
	}
	if deadline.Before(time.Now()) {
		return 0, ErrInvalidDeadline
	}

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

	productMap := make(map[int64]*catalogv1.ProductTemplate)
	for _, item := range items {
		p, err := client.GetProductTemplate(ctx, item.ProductTemplateID)
		if err != nil {
			log.Error().Err(err).Int64("product_id", item.ProductTemplateID).Msg("get product template failed")
			return 0, ErrProductInvalid
		}
		if p.StoreId != storeID {
			log.Warn().Int64("product_id", p.Id).Int64("store_id", storeID).
				Msg("product does not belong to this store")
			return 0, ErrProductInvalid
		}
		if !p.UpdatedAt.AsTime().Equal(item.UpdatedAt) {
			log.Warn().Int64("product_id", item.ProductTemplateID).
				Msg("product version mismatch")
			return 0, ErrProductInvalid
		}
		productMap[p.Id] = p
	}

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

type DemandByStoreResult struct {
	StoreID                   int64
	StoreName                 string
	ParticipantAvatars        []string
	TotalOriginUnitPriceCents int32
	TotalServiceFeeCents      int32
	UpdatedAt                 time.Time
}

func GetDemandList(
	ctx context.Context,
	page, pageSize int32,
	storeName string,
) ([]*DemandByStoreResult, int32, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	aggregations, totalCount, err := repository.GetDemandListByStore(ctx, page, pageSize, storeName)
	if err != nil {
		log.Error().Err(err).Msg("get demand list by store failed")
		return nil, 0, ErrInternal
	}

	if len(aggregations) == 0 {
		return []*DemandByStoreResult{}, 0, nil
	}

	storeMap := make(map[int64]string)
	for _, agg := range aggregations {
		if _, ok := storeMap[agg.StoreID]; ok {
			continue
		}
		store, err := client.GetStore(ctx, agg.StoreID)
		if err != nil {
			log.Warn().Err(err).Int64("store_id", agg.StoreID).Msg("get store failed, skip")
			storeMap[agg.StoreID] = ""
			continue
		}
		storeMap[agg.StoreID] = store.Name
	}

	if storeName != "" {
		filtered := make([]*repository.DemandListAggregation, 0)
		for _, agg := range aggregations {
			if name, ok := storeMap[agg.StoreID]; ok && strings.Contains(name, storeName) {
				filtered = append(filtered, agg)
			}
		}
		aggregations = filtered
	}

	allRequesterIDs := make(map[int64]struct{})
	storeRequesters := make(map[int64][]int64)
	for _, agg := range aggregations {
		requesterIDs, err := repository.GetDistinctRequestersByStore(ctx, agg.StoreID, 3)
		if err != nil {
			log.Warn().Err(err).Int64("store_id", agg.StoreID).Msg("get requesters failed")
			continue
		}
		storeRequesters[agg.StoreID] = requesterIDs
		for _, rid := range requesterIDs {
			allRequesterIDs[rid] = struct{}{}
		}
	}

	userMap := make(map[int64]*userv1.UserInfo)
	if len(allRequesterIDs) > 0 {
		ids := make([]int64, 0, len(allRequesterIDs))
		for rid := range allRequesterIDs {
			ids = append(ids, rid)
		}
		users, err := client.GetUsers(ctx, ids)
		if err != nil {
			log.Warn().Err(err).Msg("get users failed, avatars will be empty")
		} else {
			for _, u := range users {
				userMap[u.Id] = u
			}
		}
	}

	results := make([]*DemandByStoreResult, 0, len(aggregations))
	for _, agg := range aggregations {
		avatars := make([]string, 0, 3)
		for _, rid := range storeRequesters[agg.StoreID] {
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

	return results, int32(totalCount), nil
}

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

func GetDemandDetail(ctx context.Context, storeID int64) ([]*DemandDetailResult, error) {
	if storeID <= 0 {
		return nil, errors.New("invalid store_id")
	}

	items, err := repository.GetOpenDemandItemsByStore(ctx, storeID)
	if err != nil {
		log.Error().Err(err).Int64("store_id", storeID).Msg("get open demand items failed")
		return nil, ErrInternal
	}

	if len(items) == 0 {
		return []*DemandDetailResult{}, nil
	}

	productMap := make(map[int64]*DemandDetailResult)
	requesterIDSet := make(map[int64]struct{})

	for _, item := range items {
		if _, exists := productMap[item.ProductTemplateID]; !exists {
			productMap[item.ProductTemplateID] = &DemandDetailResult{
				ErrandDemandID:          item.ErrandDemandID,
				ProductTemplateID:       item.ProductTemplateID,
				EstimatedUnitPriceCents: item.EstimatedUnitPriceCents,
				TotalQuantity:           0,
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
			Deadline:               time.Time{},
			UpdatedAt:              item.UpdatedAt,
		})

		requesterIDSet[item.RequesterID] = struct{}{}
	}

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

	requesterIDs := make([]int64, 0, len(requesterIDSet))
	for rid := range requesterIDSet {
		requesterIDs = append(requesterIDs, rid)
	}

	userMap := make(map[int64]*userv1.UserInfo)
	if len(requesterIDs) > 0 {
		users, err := client.GetUsers(ctx, requesterIDs)
		if err != nil {
			log.Warn().Err(err).Msg("get users failed")
		} else {
			for _, u := range users {
				userMap[u.Id] = u
			}
		}
	}

	for _, detail := range productMap {
		for _, req := range detail.Requesters {
			if user, ok := userMap[req.RequesterID]; ok {
				req.RequesterName = user.Name
				req.RequesterAvatarURL = user.AvatarUrl
			}
		}
	}

	results := make([]*DemandDetailResult, 0, len(productMap))
	for _, detail := range productMap {
		results = append(results, detail)
	}

	return results, nil
}
