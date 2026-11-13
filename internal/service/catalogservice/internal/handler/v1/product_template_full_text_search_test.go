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

func TestProductTemplateTitleFullTextSearch(t *testing.T) {
	db := testutil.NewPostgres(t, "004_product_title_search.sql")

	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() {
		postgres.DB = previous
	})

	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `
		INSERT INTO catalog.catalog_store
			(id, name, address, created_by_user_id)
		VALUES
			(1, '测试店铺', '', 1),
			(2, '其他店铺', '', 1);

		INSERT INTO catalog.catalog_product_template
			(id, store_id, title, description, price_cents, created_by_user_id, status)
		VALUES
			(10, 1, '香氛洗衣液', '', 100, 1, 'active'),
			(20, 1, '洗衣液', '', 100, 1, 'active'),
			(30, 1, '香氛', '', 100, 1, 'active'),
			(40, 1, '牛奶', '洗衣液', 100, 1, 'active'),
			(50, 1, '面包', '', 100, 1, 'active'),
			(60, 2, '香氛洗衣液', '', 100, 1, 'active'),
			(70, 1, '香氛洗衣液', '', 100, 1, 'removed');

		INSERT INTO catalog.catalog_product_barcode
			(product_template_id, barcode)
		VALUES
			(50, '洗衣液-690');
	`); err != nil {
		t.Fatal(err)
	}

	_, handler := catalogv1connect.NewProductTemplateServiceHandler(
		&ProductTemplateServiceServer{},
	)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	rpc := catalogv1connect.NewProductTemplateServiceClient(
		server.Client(),
		server.URL,
	)
	const (
		fullText = catalogv1.ProductTemplateSearchMode_PRODUCT_TEMPLATE_SEARCH_MODE_TITLE_FULL_TEXT
		literal  = catalogv1.ProductTemplateSearchMode_PRODUCT_TEMPLATE_SEARCH_MODE_UNSPECIFIED
	)

	tests := []struct {
		name    string
		mode    catalogv1.ProductTemplateSearchMode
		keyword string
		page    int32
		size    int32
		ids     []int64
		total   int32
	}{
		{
			name:    "OR matching and relevance before descending ID",
			mode:    fullText,
			keyword: "香氛洗衣液",
			page:    1,
			size:    20,
			ids:     []int64{10, 30, 20},
			total:   3,
		},
		{
			name:    "first matching page",
			mode:    fullText,
			keyword: "香氛洗衣液",
			page:    1,
			size:    2,
			ids:     []int64{10, 30},
			total:   3,
		},
		{
			name:    "second matching page",
			mode:    fullText,
			keyword: "香氛洗衣液",
			page:    2,
			size:    2,
			ids:     []int64{20},
			total:   3,
		},
		{
			name:    "page beyond matching results",
			mode:    fullText,
			keyword: "香氛洗衣液",
			page:    3,
			size:    2,
			ids:     nil,
			total:   3,
		},
		{
			name:    "punctuation produces no searchable terms",
			mode:    fullText,
			keyword: "!!!",
			page:    1,
			size:    20,
			ids:     nil,
			total:   0,
		},
		{
			name:    "blank keyword keeps ordinary listing",
			mode:    fullText,
			keyword: " \t\n ",
			page:    1,
			size:    20,
			ids:     []int64{50, 40, 30, 20, 10},
			total:   5,
		},
		{
			name:    "default mode keeps description and barcode search",
			mode:    literal,
			keyword: "洗衣液",
			page:    1,
			size:    20,
			ids:     []int64{50, 40, 20, 10},
			total:   4,
		},
		{
			name:    "apostrophe is ordinary input",
			mode:    fullText,
			keyword: "香氛 ' 洗衣液",
			page:    1,
			size:    20,
			ids:     []int64{10, 30, 20},
			total:   3,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := rpc.GetProductTemplateList(
				ctx,
				connect.NewRequest(&catalogv1.GetProductTemplateListRequest{
					StoreId:    1,
					Page:       test.page,
					PageSize:   test.size,
					Keyword:    test.keyword,
					SearchMode: test.mode,
				}),
			)
			if err != nil {
				t.Fatal(err)
			}

			ids := make([]int64, 0, len(response.Msg.ProductTemplates))
			for _, product := range response.Msg.ProductTemplates {
				ids = append(ids, product.Id)
			}

			if !slices.Equal(ids, test.ids) {
				t.Errorf("got IDs %v; want %v", ids, test.ids)
			}
			if response.Msg.TotalCount != test.total {
				t.Errorf(
					"got total %d; want %d",
					response.Msg.TotalCount,
					test.total,
				)
			}
			if response.Msg.CurrentPage != test.page {
				t.Errorf(
					"got page %d; want %d",
					response.Msg.CurrentPage,
					test.page,
				)
			}
		})
	}
}
