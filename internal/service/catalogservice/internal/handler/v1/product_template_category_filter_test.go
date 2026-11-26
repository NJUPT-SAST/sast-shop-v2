package v1

import (
	"context"
	"net/http/httptest"
	"slices"
	"testing"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/catalog/v1/catalogv1connect"
	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
)

func TestProductTemplateCategoryFilterIncludesRootAndChildren(t *testing.T) {
	db := testutil.NewPostgres(t, "006_admin_catalog_spot.sql", "010_store_product_categories.sql")
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = previous })
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO catalog.catalog_store (id, name, address, created_by_user_id)
		VALUES (1, 'Campus Shop', '', 1), (2, 'Other Shop', '', 1);
		INSERT INTO catalog.catalog_product_category (id, store_id, parent_id, name, sort_order)
		VALUES (100, 1, NULL, 'Food', 0), (101, 1, 100, 'Snacks', 0),
		       (102, 1, 100, 'Drinks', 1), (200, 2, NULL, 'Food', 0);
		INSERT INTO catalog.catalog_product_template
			(id, store_id, category_id, title, description, price_cents, created_by_user_id, status)
		VALUES (10, 1, 100, 'Root item', '', 100, 1, 'active'),
		       (11, 1, 101, 'Child item', '', 100, 1, 'active'),
		       (12, 1, 102, 'Other child item', '', 100, 1, 'active'),
		       (20, 2, 200, 'Other store item', '', 100, 1, 'active');
	`); err != nil {
		t.Fatal(err)
	}
	_, handler := catalogv1connect.NewProductTemplateServiceHandler(&ProductTemplateServiceServer{})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	rpc := catalogv1connect.NewProductTemplateServiceClient(server.Client(), server.URL)
	list := func(categoryID int64) []int64 {
		t.Helper()
		response, err := rpc.GetProductTemplateList(ctx, connect.NewRequest(&catalogv1.GetProductTemplateListRequest{
			StoreId:            1,
			Page:               1,
			PageSize:           20,
			CategoryFilterMode: catalogv1.ProductTemplateCategoryFilterMode_PRODUCT_TEMPLATE_CATEGORY_FILTER_MODE_CATEGORY,
			CategoryId:         categoryID,
		}))
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]int64, 0, len(response.Msg.ProductTemplates))
		for _, product := range response.Msg.ProductTemplates {
			ids = append(ids, product.Id)
		}
		return ids
	}
	if ids := list(100); !slices.Equal(ids, []int64{12, 11, 10}) {
		t.Fatalf("root category returned %v, want direct and child products [12 11 10]", ids)
	}
	if ids := list(101); !slices.Equal(ids, []int64{11}) {
		t.Fatalf("child category returned %v, want only [11]", ids)
	}
	if ids := list(102); !slices.Equal(ids, []int64{12}) {
		t.Fatalf("sibling category returned %v, want only [12]", ids)
	}
	_, err := rpc.GetProductTemplateList(ctx, connect.NewRequest(&catalogv1.GetProductTemplateListRequest{
		StoreId: 1, Page: 1, PageSize: 20,
		CategoryFilterMode: catalogv1.ProductTemplateCategoryFilterMode_PRODUCT_TEMPLATE_CATEGORY_FILTER_MODE_CATEGORY,
		CategoryId:         200,
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("cross-store category filter error = %v, want invalid argument", err)
	}
}
