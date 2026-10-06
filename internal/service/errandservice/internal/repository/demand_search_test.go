package repository

import (
	"context"
	"slices"
	"testing"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
)

func TestGetDemandListByStoreSearchBeforePagination(t *testing.T) {
	db := testutil.NewPostgres(t)
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = previous })
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO catalog.catalog_store (id, name, address, created_by_user_id)
		VALUES (1, 'Newest Unrelated Shop', '', 1), (2, 'Campus Shop A', '', 1),
		(3, 'campus Shop B', '', 1), (4, '100% pure_under\score O''Reilly', '', 1),
		(5, 'CAMPUS Shop C', '', 1), (6, 'Campus Closed', '', 1),
		(7, '校园小卖部', '', 1);
		INSERT INTO errand.errand_demand (id, requester_id, store_id, deadline)
		SELECT id, 1, id, now() + interval '1 day' FROM catalog.catalog_store;
		INSERT INTO errand.errand_demand_item
		(id, errand_demand_id, requester_id, store_id, product_template_id,
		 estimated_unit_price_cents, quantity, service_fee_per_unit_cents, status, updated_at)
		VALUES
		(1, 1, 1, 1, 1, 100, 1, 10, 'open', '2026-10-06 12:00:00Z'),
		(2, 2, 1, 2, 2, 100, 2, 10, 'open', '2026-10-06 11:00:00Z'),
		(3, 2, 2, 2, 3, 200, 3, 20, 'open', '2026-10-06 10:00:00Z'),
		(4, 3, 1, 3, 4, 300, 1, 30, 'open', '2026-10-06 10:00:00Z'),
		(5, 4, 1, 4, 5, 400, 1, 40, 'open', '2026-10-06 09:00:00Z'),
		(6, 5, 1, 5, 6, 500, 1, 50, 'open', '2026-10-06 10:00:00Z'),
		(7, 6, 1, 6, 7, 600, 1, 60, 'cancelled', '2026-10-06 13:00:00Z'),
		(8, 7, 1, 7, 8, 700, 1, 70, 'open', '2026-10-06 08:00:00Z');
	`); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name     string
		keyword  string
		page     int32
		pageSize int32
		storeIDs []int64
		total    int
	}{
		{"blank preserves all open stores", "", 1, 20, []int64{1, 2, 3, 5, 4, 7}, 6},
		{"whitespace is blank", "　 \t ", 1, 20, []int64{1, 2, 3, 5, 4, 7}, 6},
		{"case insensitive trimmed first page", "  cAmPuS  ", 1, 1, []int64{2}, 3},
		{"second matching page", "campus", 2, 1, []int64{3}, 3},
		{"stable third page for tied timestamp", "campus", 3, 1, []int64{5}, 3},
		{"page beyond matching results retains count", "campus", 4, 1, nil, 3},
		{"counts stores rather than demand items", "Shop A", 1, 20, []int64{2}, 1},
		{"Chinese substring", "小卖", 1, 20, []int64{7}, 1},
		{"literal percent", "%", 1, 20, []int64{4}, 1},
		{"literal underscore", "_", 1, 20, []int64{4}, 1},
		{"literal backslash", `\`, 1, 20, []int64{4}, 1},
		{"apostrophe", "'", 1, 20, []int64{4}, 1},
		{"SQL syntax is literal", "' OR 1=1 --", 1, 20, nil, 0},
		{"closed demand does not match", "Closed", 1, 20, nil, 0},
		{"no matching store", "missing", 1, 20, nil, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			results, total, err := GetDemandListByStore(ctx, test.page, test.pageSize, test.keyword)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]int64, 0, len(results))
			for _, result := range results {
				ids = append(ids, result.StoreID)
				if result.StoreID == 2 &&
					(result.TotalOriginUnitPriceCents != 800 || result.TotalServiceFeeCents != 80) {
					t.Fatalf(
						"wrong aggregate amounts: product=%d, fee=%d",
						result.TotalOriginUnitPriceCents,
						result.TotalServiceFeeCents,
					)
				}
			}
			if !slices.Equal(ids, test.storeIDs) || total != test.total {
				t.Fatalf("got stores %v, total %d; want %v, %d", ids, total, test.storeIDs, test.total)
			}
		})
	}
}
