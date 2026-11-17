package service

import (
	"context"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/repository"
	"github.com/uptrace/bun"
)

func lockDemandCatalogForSale(ctx context.Context, tx bun.Tx, storeID int64, items []DemandItemDraft) error {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ProductTemplateID)
	}
	return repository.LockCatalogForSale(ctx, tx, storeID, ids)
}

func lockDemandSelectionCatalog(
	ctx context.Context,
	tx bun.Tx,
	storeID int64,
	rows []repository.SelectedDemandItemRow,
) error {
	ids := make([]int64, 0, len(rows))
	for _, item := range rows {
		ids = append(ids, item.ProductTemplateID)
	}
	return repository.LockCatalogForSale(ctx, tx, storeID, ids)
}
