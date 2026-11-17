package repository

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"connectrpc.com/connect"
	"github.com/uptrace/bun"
)

// Locks the authoritative catalog rows while creating or replacing a demand.
func LockCatalogForSale(ctx context.Context, db bun.IDB, storeID int64, productIDs []int64) error {
	ids := slices.Clone(productIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) == 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("请至少选择一种商品"))
	}
	for _, productID := range ids {
		var id int64
		err := db.NewRaw(`SELECT cpt.id FROM catalog.catalog_store cs
 JOIN catalog.catalog_product_template cpt ON cpt.store_id=cs.id
 WHERE cs.id=? AND cpt.id=? AND cs.moderation_status='allowed'
 AND cpt.moderation_status='allowed' AND cs.status!='removed' AND cpt.status!='removed'
 FOR SHARE OF cs,cpt`, storeID, productID).Scan(ctx, &id)
		if errors.Is(err, sql.ErrNoRows) {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("店铺或商品模板当前不可交易"))
		}
		if err != nil {
			return err
		}
	}
	return nil
}
