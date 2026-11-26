package repository

import (
	"strings"
	"testing"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/model"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

func TestCategoryFilterIncludesDirectAndChildCategoryIDs(t *testing.T) {
	query := filterProductTemplates(
		bun.NewDB(nil, pgdialect.New()).NewSelect().Model((*model.CatalogProductTemplate)(nil)),
		1, "", false, 2, 100,
	)
	sql := query.String()
	if !strings.Contains(sql, "cpt.category_id = 100") ||
		!strings.Contains(sql, "child.parent_id = 100") ||
		!strings.Contains(sql, "child.store_id = cpt.store_id") {
		t.Fatalf("category filter SQL does not include direct and child category IDs: %s", sql)
	}
}
