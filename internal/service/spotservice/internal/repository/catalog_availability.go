package repository

import (
	"context"
	"database/sql"
	"errors"

	"connectrpc.com/connect"
	"github.com/uptrace/bun"
)

// The shared locks prevent catalog moderation from racing a new sale.
func LockCatalogForSale(ctx context.Context, db bun.IDB, storeID, productID int64) error {
	var id int64
	err := db.NewRaw(`SELECT cpt.id FROM catalog.catalog_store cs
        JOIN catalog.catalog_product_template cpt ON cpt.store_id = cs.id
        WHERE cs.id = ? AND cpt.id = ? AND cs.moderation_status = 'allowed'
        AND cpt.moderation_status = 'allowed' AND cs.status != 'removed' AND cpt.status != 'removed'
        FOR SHARE OF cs, cpt`, storeID, productID).Scan(ctx, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("店铺或商品模板当前不可交易"))
	}
	return err
}
