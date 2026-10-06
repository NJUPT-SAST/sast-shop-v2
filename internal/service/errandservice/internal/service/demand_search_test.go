package service

import (
	"context"
	"testing"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/catalog/v1/catalogv1connect"
	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/user/v1/userv1connect"
	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/client"
)

type demandSearchCatalogClient struct {
	catalogv1connect.CatalogInternalServiceClient
}

func (demandSearchCatalogClient) GetStore(_ context.Context, request *connect.Request[catalogv1.GetStoreRequest]) (*connect.Response[catalogv1.GetStoreResponse], error) {
	return connect.NewResponse(&catalogv1.GetStoreResponse{
		Store: &catalogv1.Store{Id: request.Msg.StoreId, Name: "Campus Shop"},
	}), nil
}

type demandSearchUserClient struct {
	userv1connect.UserInternalServiceClient
}

func (demandSearchUserClient) GetUsers(context.Context, *connect.Request[userv1.GetUsersRequest]) (*connect.Response[userv1.GetUsersResponse], error) {
	return connect.NewResponse(&userv1.GetUsersResponse{}), nil
}

func TestGetDemandListPreservesSearchPagination(t *testing.T) {
	db := testutil.NewPostgres(t)
	previousDB := postgres.DB
	previousCatalog := client.CatalogInternalClient
	previousUser := client.UserInternalClient
	postgres.DB = db
	client.CatalogInternalClient = demandSearchCatalogClient{}
	client.UserInternalClient = demandSearchUserClient{}
	t.Cleanup(func() {
		postgres.DB = previousDB
		client.CatalogInternalClient = previousCatalog
		client.UserInternalClient = previousUser
	})
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO catalog.catalog_store (id, name, address, created_by_user_id)
		VALUES (1, 'Campus Shop', '', 1);
		INSERT INTO errand.errand_demand (id, requester_id, store_id, deadline)
		VALUES (1, 1, 1, now() + interval '1 day');
		INSERT INTO errand.errand_demand_item
		(errand_demand_id, requester_id, store_id, product_template_id,
		 estimated_unit_price_cents, quantity, service_fee_per_unit_cents)
		VALUES (1, 1, 1, 1, 200, 2, 10);
	`); err != nil {
		t.Fatal(err)
	}

	t.Run("does not refilter an already matched store with case sensitive comparison", func(t *testing.T) {
		results, total, err := GetDemandList(ctx, 1, 1, " campus ")
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || total != 1 || results[0].StoreName != "Campus Shop" || results[0].TotalOriginUnitPriceCents != 400 {
			t.Fatalf("got results %v, total %d; want matching Campus Shop and total 1", results, total)
		}
	})
	t.Run("retains matching total on an empty page", func(t *testing.T) {
		results, total, err := GetDemandList(ctx, 2, 1, "campus")
		if err != nil {
			t.Fatal(err)
		}
		if results == nil || len(results) != 0 || total != 1 {
			t.Fatalf("got results %v, total %d; want empty results and total 1", results, total)
		}
	})
	t.Run("reports no match", func(t *testing.T) {
		results, total, err := GetDemandList(ctx, 1, 1, "missing")
		if err != nil {
			t.Fatal(err)
		}
		if results == nil || len(results) != 0 || total != 0 {
			t.Fatalf("got results %v, total %d; want empty results and total 0", results, total)
		}
	})
}
