package v1

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/catalog/v1/catalogv1connect"
	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/spot/v1/spotv1connect"
	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/client"
)

type searchCatalogClient struct {
	catalogv1connect.CatalogInternalServiceClient
	requests chan []int64
}

func (c *searchCatalogClient) GetProductTemplates(
	_ context.Context,
	request *connect.Request[catalogv1.GetProductTemplatesRequest],
) (*connect.Response[catalogv1.GetProductTemplatesResponse], error) {
	c.requests <- slices.Clone(request.Msg.ProductTemplateIds)
	templates := make([]*catalogv1.ProductTemplate, 0, len(request.Msg.ProductTemplateIds))
	for _, id := range request.Msg.ProductTemplateIds {
		templates = append(templates, &catalogv1.ProductTemplate{Id: id})
	}
	return connect.NewResponse(&catalogv1.GetProductTemplatesResponse{ProductTemplates: templates}), nil
}

func TestSpotGoodsSearch(t *testing.T) {
	db := testutil.NewPostgres(t)
	previousDB, previousCatalog := postgres.DB, client.CatalogInternalServiceClient
	postgres.DB = db
	catalog := &searchCatalogClient{requests: make(chan []int64, 1)}
	client.CatalogInternalServiceClient = catalog
	t.Cleanup(func() {
		postgres.DB = previousDB
		client.CatalogInternalServiceClient = previousCatalog
	})
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO catalog.catalog_store (id, name, address, created_by_user_id)
		VALUES (1, 'Campus Shop', '', 1), (2, 'Beta Market', '', 1);
		INSERT INTO catalog.catalog_product_template
		(id, store_id, title, description, price_cents, created_by_user_id)
		VALUES (10, 1, 'Water Bottle', 'cold DRINK', 100, 1),
		(20, 2, 'Water Cup', 'warm drink', 100, 1),
		(30, 1, 'Milk', 'Fresh', 100, 1),
		(40, 2, '100% pure_under\score', 'literal', 100, 1),
		(60, 2, '中文矿泉水', '中文规格', 100, 1),
		(70, 2, 'O''Reilly', '', 100, 1);
		INSERT INTO catalog.catalog_product_barcode (product_template_id, barcode)
		VALUES (10, '690-A'), (20, '690-B'), (20, '690-B-extra');
		INSERT INTO spot.spot_goods
		(id, seller_id, store_id, product_template_id, sale_price_cents, stock_total, closed_at, created_at)
		VALUES (101, 1, 1, 10, 100, 1, NULL, '2026-10-06'),
		(102, 1, 2, 20, 100, 1, NULL, '2026-10-06'),
		(103, 1, 1, 10, 100, 0, NULL, '2026-10-06'),
		(104, 1, 1, 30, 100, 1, NULL, '2026-10-06'),
		(105, 1, 2, 40, 100, 1, NULL, '2026-10-06'),
		(106, 1, 1, 10, 100, 1, '2026-10-06', '2026-10-06'),
		(107, 1, 2, 60, 100, 1, NULL, '2026-10-06'),
		(108, 1, 2, 70, 100, 1, NULL, '2026-10-06');
	`); err != nil {
		t.Fatal(err)
	}
	_, handler := spotv1connect.NewSpotGoodsServiceHandler(&SpotGoodsServiceServer{})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	rpc := spotv1connect.NewSpotGoodsServiceClient(server.Client(), server.URL)
	for _, test := range []struct {
		name    string
		storeID int64
		keyword string
		page    int32
		size    int32
		ids     []int64
		total   int32
	}{
		{"legacy blank includes open zero stock", 0, "", 1, 20, []int64{108, 107, 105, 104, 103, 102, 101}, 7},
		{"case insensitive title before pagination", 0, " WATER ", 1, 2, []int64{103, 102}, 3},
		{"second matching page", 0, "water", 2, 2, []int64{101}, 3},
		{"page beyond matching results", 0, "water", 3, 2, nil, 3},
		{"store scope", 1, "water", 1, 20, []int64{103, 101}, 2},
		{"description", 0, "DrInK", 1, 20, []int64{103, 102, 101}, 3},
		{"shop name", 0, "CaMpUs", 1, 20, []int64{104, 103, 101}, 3},
		{"shop name with incompatible scope", 2, "campus", 1, 20, nil, 0},
		{"multiple barcodes do not duplicate listings", 0, "690", 1, 20, []int64{103, 102, 101}, 3},
		{"literal percent", 0, "%", 1, 20, []int64{105}, 1},
		{"literal underscore", 0, "_", 1, 20, []int64{105}, 1},
		{"literal backslash", 0, `\`, 1, 20, []int64{105}, 1},
		{"Chinese substring", 0, "矿泉", 1, 20, []int64{107}, 1},
		{"apostrophe", 0, "'", 1, 20, []int64{108}, 1},
		{"SQL syntax is literal", 0, "' OR 1=1 --", 1, 20, nil, 0},
		{"missing keyword", 0, "missing", 1, 20, nil, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := rpc.ListSpotGoods(ctx, connect.NewRequest(&spotv1.ListSpotGoodsRequest{
				StoreId: test.storeID, Keyword: test.keyword, Page: test.page, PageSize: test.size,
			}))
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]int64, 0, len(response.Msg.SpotGoodsList))
			var templateIDs []int64
			for _, goods := range response.Msg.SpotGoodsList {
				ids = append(ids, goods.Id)
				if !slices.Contains(templateIDs, goods.ProductTemplate.Id) {
					templateIDs = append(templateIDs, goods.ProductTemplate.Id)
				}
			}
			if !slices.Equal(ids, test.ids) || response.Msg.TotalCount != test.total ||
				response.Msg.CurrentPage != test.page {
				t.Fatalf("got ids %v, total %d, page %d; want %v, %d, %d",
					ids, response.Msg.TotalCount, response.Msg.CurrentPage, test.ids, test.total, test.page)
			}
			select {
			case requestedIDs := <-catalog.requests:
				if len(test.ids) == 0 || !slices.Equal(requestedIDs, templateIDs) {
					t.Fatalf("hydrated IDs %v, want current page only: %v", requestedIDs, templateIDs)
				}
			default:
				if len(test.ids) != 0 {
					t.Fatal("missing catalog hydration request")
				}
			}
		})
	}
}

func TestSpotGoodsSearchRejectsInvalidParameters(t *testing.T) {
	for _, request := range []*spotv1.ListSpotGoodsRequest{
		{Page: 1, PageSize: 20, StoreId: -1},
		{Page: 1, PageSize: 20, Keyword: strings.Repeat("水", 201)},
	} {
		_, err := (&SpotGoodsServiceServer{}).ListSpotGoods(context.Background(), connect.NewRequest(request))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("request %v: got %v, want invalid_argument", request, err)
		}
	}
}
