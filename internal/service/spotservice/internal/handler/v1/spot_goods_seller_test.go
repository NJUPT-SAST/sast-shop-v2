package v1

import (
	"context"
	"errors"
	"net/http/httptest"
	"slices"
	"testing"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/spot/v1/spotv1connect"
	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/user/v1/userv1connect"
	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	rpcinterceptor "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/client"
	"github.com/rs/zerolog/log"
)

type sellerListUserClient struct {
	userv1connect.UserInternalServiceClient
	requests [][]int64
}

func (c *sellerListUserClient) GetUsers(
	_ context.Context,
	request *connect.Request[userv1.GetUsersRequest],
) (*connect.Response[userv1.GetUsersResponse], error) {
	c.requests = append(c.requests, slices.Clone(request.Msg.UserIds))
	users := make([]*userv1.UserInfo, 0, len(request.Msg.UserIds))
	for _, id := range request.Msg.UserIds {
		users = append(users, &userv1.UserInfo{Id: id, Name: "seller"})
	}
	return connect.NewResponse(&userv1.GetUsersResponse{Users: users}), nil
}

type sellerListSessions struct{}

type sellerListCase struct {
	name     string
	token    string
	sellerID int64
	page     int32
	ids      []int64
	total    int32
}

func (sellerListSessions) GetSession(_ context.Context, token string) (*rpcinterceptor.AuthUser, error) {
	switch token {
	case "first-seller":
		return &rpcinterceptor.AuthUser{UserID: 42, Status: "active", Role: "user"}, nil
	case "second-seller":
		return &rpcinterceptor.AuthUser{UserID: 43, Status: "active", Role: "user"}, nil
	case "empty-seller":
		return &rpcinterceptor.AuthUser{UserID: 44, Status: "active", Role: "user"}, nil
	}
	return nil, errors.New("session missing")
}

func (sellerListSessions) GetUserByID(context.Context, int64) (*rpcinterceptor.AuthUser, error) {
	return nil, errors.New("development bypass disabled")
}

func (sellerListSessions) SaveSession(context.Context, string, *rpcinterceptor.AuthUser) error {
	return nil
}

func TestListMySpotGoodsFiltersBeforePagination(t *testing.T) {
	db := testutil.NewPostgres(t)
	previousDB, previousCatalog, previousUser := postgres.DB, client.CatalogInternalServiceClient, client.UserInternalServiceClient
	postgres.DB = db
	catalog := &searchCatalogClient{requests: make(chan []int64, 1)}
	user := &sellerListUserClient{}
	client.CatalogInternalServiceClient, client.UserInternalServiceClient = catalog, user
	t.Cleanup(func() {
		postgres.DB, client.CatalogInternalServiceClient, client.UserInternalServiceClient = previousDB, previousCatalog, previousUser
	})
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
    INSERT INTO spot.spot_goods
    (id, seller_id, store_id, product_template_id, sale_price_cents, stock_total, closed_at, created_at, updated_at)
    VALUES (101, 42, 1, 10, 100, -1, NULL, '2026-10-07', '2026-10-07 01:00:00.123456+00'),
    (102, 43, 1, 20, 200, 9, NULL, '2026-10-07', '2026-10-07 01:00:00+00'),
    (103, 42, 2, 30, 300, 0, NULL, '2026-10-07', '2026-10-07 01:00:00.654321+00'),
    (104, 42, 1, 10, 100, 7, '2026-10-07', '2026-10-07', '2026-10-07 01:00:00+00'),
    (105, 43, 2, 40, 400, 9, NULL, '2026-10-08', '2026-10-07 01:00:00+00');
  `); err != nil {
		t.Fatal(err)
	}
	_, handler := spotv1connect.NewSpotGoodsServiceHandler(&SpotGoodsServiceServer{}, connect.WithInterceptors(
		rpcinterceptor.AuthRequired(sellerListSessions{}, log.Logger, false),
	))
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	rpc := spotv1connect.NewSpotGoodsServiceClient(server.Client(), server.URL)
	for _, test := range []sellerListCase{
		{"first seller page includes zero stock", "first-seller", 42, 1, []int64{103}, 2},
		{"first seller second page includes delisted goods", "first-seller", 42, 2, []int64{101}, 2},
		{"first seller beyond final page", "first-seller", 42, 3, nil, 2},
		{"different session changes seller", "second-seller", 43, 1, []int64{105}, 2},
		{"no seller listings", "empty-seller", 44, 1, nil, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := connect.NewRequest(&spotv1.ListMySpotGoodsRequest{Page: test.page, PageSize: 1})
			request.Header().Set("Authorization", "Bearer "+test.token)
			request.Header().Set("X-Dev-User-ID", "43")
			response, err := rpc.ListMySpotGoods(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			assertSellerListPage(t, response.Msg, test)
			select {
			case requested := <-catalog.requests:
				if len(test.ids) == 0 || len(requested) != 1 {
					t.Fatalf("unexpected template hydration: %v", requested)
				}
			default:
				if len(test.ids) > 0 {
					t.Fatal("missing page template hydration")
				}
			}
		})
	}
	if len(user.requests) != 3 {
		t.Fatalf("expected one seller hydration per nonempty page, got %v", user.requests)
	}
	_, err := rpc.ListMySpotGoods(ctx, connect.NewRequest(&spotv1.ListMySpotGoodsRequest{Page: 1, PageSize: 20}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("unauthenticated request returned %v", err)
	}
}

func assertSellerListPage(t *testing.T, response *spotv1.ListMySpotGoodsResponse, test sellerListCase) {
	t.Helper()
	ids := make([]int64, 0, len(response.SpotGoodsList))
	for _, goods := range response.SpotGoodsList {
		ids = append(ids, goods.Id)
		if goods.Seller.GetId() != test.sellerID || goods.UpdatedAt == nil {
			t.Fatalf("missing or mismatched seller/version: %v", goods)
		}
		if goods.Id == 101 && goods.Stock != -1 {
			t.Fatalf("delisted stock must be preserved for management: %v", goods)
		}
		if goods.Id == 103 &&
			(goods.Stock != 0 || goods.ProductTemplate.StoreId != 2 || goods.UpdatedAt.Nanos != 654321000) {
			t.Fatalf("zero stock, original store and precise version must be preserved: %v", goods)
		}
	}
	if !slices.Equal(ids, test.ids) || response.TotalCount != test.total || response.CurrentPage != test.page {
		t.Fatalf("got IDs %v, total %d, page %d; want %v, %d, %d", ids, response.TotalCount,
			response.CurrentPage, test.ids, test.total, test.page)
	}
}

func TestListMySpotGoodsRejectsInvalidParametersAndMissingIdentity(t *testing.T) {
	handler := &SpotGoodsServiceServer{}
	ctx := rpcinterceptor.SetUserToContext(context.Background(), &rpcinterceptor.AuthUser{UserID: 42})
	for _, request := range []*spotv1.ListMySpotGoodsRequest{
		{Page: 0, PageSize: 20},
		{Page: -1, PageSize: 20},
		{Page: 1, PageSize: 0},
		{Page: 1, PageSize: 101},
	} {
		_, err := handler.ListMySpotGoods(ctx, connect.NewRequest(request))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("request %v returned %v", request, err)
		}
	}
	_, err := handler.ListMySpotGoods(
		context.Background(),
		connect.NewRequest(&spotv1.ListMySpotGoodsRequest{Page: 1, PageSize: 20}),
	)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("missing session returned %v", err)
	}
}
