package v1

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/catalog/v1/catalogv1connect"
	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
)

func TestProductTemplateSearch(t *testing.T) {
	db := testutil.NewPostgres(t)
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = previous })
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO catalog.catalog_store (id, name, address, created_by_user_id)
		VALUES (1, 'Campus Shop', '', 1), (2, 'Beta Market', '', 1);
		INSERT INTO catalog.catalog_product_template
		(id, store_id, title, description, price_cents, created_by_user_id, status)
		VALUES (10, 1, 'Water Bottle', 'cold DRINK', 100, 1, 'active'),
		(20, 2, 'Water Cup', 'warm drink', 100, 1, 'active'),
		(30, 1, 'Milk', 'Fresh', 100, 1, 'hidden'),
		(40, 2, '100% pure_under\score', 'literal', 100, 1, 'active'),
		(50, 1, 'Water removed', '', 100, 1, 'removed'),
		(60, 2, '中文矿泉水', '中文规格', 100, 1, 'active'),
		(70, 2, 'O''Reilly', '', 100, 1, 'active');
		INSERT INTO catalog.catalog_product_barcode (product_template_id, barcode)
		VALUES (10, '690-A'), (20, '690-B'), (20, '690-B-extra'), (50, '690-removed');
	`); err != nil {
		t.Fatal(err)
	}
	_, handler := catalogv1connect.NewProductTemplateServiceHandler(&ProductTemplateServiceServer{})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	rpc := catalogv1connect.NewProductTemplateServiceClient(server.Client(), server.URL)
	for _, test := range []struct {
		name    string
		storeID int64
		keyword string
		page    int32
		size    int32
		ids     []int64
		total   int32
	}{
		{"legacy blank across stores", 0, "", 1, 20, []int64{70, 60, 40, 30, 20, 10}, 6},
		{"case insensitive title before pagination", 0, " WATER ", 1, 1, []int64{20}, 2},
		{"second matching page", 0, "water", 2, 1, []int64{10}, 2},
		{"page beyond matching results", 0, "water", 3, 1, nil, 2},
		{"store scope", 1, "water", 1, 20, []int64{10}, 1},
		{"description", 0, "DrInK", 1, 20, []int64{20, 10}, 2},
		{"multiple barcodes do not duplicate templates", 0, "690", 1, 20, []int64{20, 10}, 2},
		{"literal percent", 0, "%", 1, 20, []int64{40}, 1},
		{"literal underscore", 0, "_", 1, 20, []int64{40}, 1},
		{"literal backslash", 0, `\`, 1, 20, []int64{40}, 1},
		{"Chinese substring", 0, "矿泉", 1, 20, []int64{60}, 1},
		{"apostrophe", 0, "'", 1, 20, []int64{70}, 1},
		{"SQL syntax is literal", 0, "' OR 1=1 --", 1, 20, nil, 0},
		{"missing keyword", 0, "missing", 1, 20, nil, 0},
		{"shop name is not a template search field", 0, "Campus", 1, 20, nil, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := rpc.GetProductTemplateList(
				ctx,
				connect.NewRequest(&catalogv1.GetProductTemplateListRequest{
					StoreId: test.storeID, Keyword: test.keyword, Page: test.page, PageSize: test.size,
				}),
			)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]int64, 0, len(response.Msg.ProductTemplates))
			for _, template := range response.Msg.ProductTemplates {
				ids = append(ids, template.Id)
			}
			if !slices.Equal(ids, test.ids) || response.Msg.TotalCount != test.total ||
				response.Msg.CurrentPage != test.page {
				t.Fatalf("got ids %v, total %d, page %d; want %v, %d, %d",
					ids, response.Msg.TotalCount, response.Msg.CurrentPage, test.ids, test.total, test.page)
			}
		})
	}
}

func TestProductTemplateSearchRejectsInvalidParameters(t *testing.T) {
	for _, request := range []*catalogv1.GetProductTemplateListRequest{
		{Page: 1, PageSize: 20, StoreId: -1},
		{Page: 0, PageSize: 20},
		{Page: 1, PageSize: 0},
		{Page: 1, PageSize: 101},
		{Page: 1, PageSize: 20, Keyword: strings.Repeat("水", 201)},
	} {
		_, err := (&ProductTemplateServiceServer{}).GetProductTemplateList(
			context.Background(),
			connect.NewRequest(request),
		)
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("request %v: got %v, want invalid_argument", request, err)
		}
	}
}
