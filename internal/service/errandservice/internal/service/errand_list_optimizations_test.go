package service

import (
	"context"
	"slices"
	"testing"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/catalog/v1/catalogv1connect"
	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/client"
)

type buyerOrderSearchCatalogClient struct {
	catalogv1connect.CatalogInternalServiceClient
}

func (buyerOrderSearchCatalogClient) GetStore(
	_ context.Context,
	req *connect.Request[catalogv1.GetStoreRequest],
) (*connect.Response[catalogv1.GetStoreResponse], error) {
	return connect.NewResponse(&catalogv1.GetStoreResponse{
		Store: &catalogv1.Store{Id: req.Msg.StoreId, Name: "Store"},
	}), nil
}

func (buyerOrderSearchCatalogClient) GetProductTemplate(
	_ context.Context,
	req *connect.Request[catalogv1.GetProductTemplateRequest],
) (*connect.Response[catalogv1.GetProductTemplateResponse], error) {
	return connect.NewResponse(&catalogv1.GetProductTemplateResponse{
		ProductTemplate: &catalogv1.ProductTemplate{Id: req.Msg.ProductTemplateId, Title: "Product"},
	}), nil
}

func TestGetErrandTaskListMultiStatusKeywordAndParticipants(t *testing.T) {
	db := testutil.NewPostgres(
		t,
		"002_west_pocket.sql",
		"005_admin_identity.sql",
		"006_admin_catalog_spot.sql",
		"007_admin_corrections.sql",
		"008_admin_errand_pocket.sql",
	)
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = previous })
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO "user".user_account (id, feishu_open_id, display_name, avatar_url)
		VALUES (1, 'u1', 'A', '/a.png'), (2, 'u2', 'B', ''), (3, 'u3', 'C', '/c.png'), (4, 'u4', 'D', '/d.png');
		INSERT INTO catalog.catalog_store (id, name, address, created_by_user_id)
		VALUES (1, 'Campus Shop', '', 1), (2, '100% pure_shop', '', 1);
		INSERT INTO errand.errand_task (id, task_no, captain_id, store_id, status, created_at)
		VALUES
			(10, 'ET-ALPHA', 1, 1, 'shopping', '2026-10-06 10:00:00Z'),
			(20, 'ET-BETA', 1, 2, 'distributing', '2026-10-06 11:00:00Z'),
			(30, 'ET-DONE', 1, 1, 'completed', '2026-10-06 12:00:00Z');
		INSERT INTO errand.errand_task_item (id, task_id, product_template_id, title_snapshot, description_snapshot, required_quantity, deadline)
		VALUES
			(101, 10, 1, 'Iced Coffee', '500ml', 2, now()),
			(102, 20, 2, 'Matcha Cake', 'sweet', 1, now()),
			(103, 30, 3, 'Green Tea', 'bottle', 1, now());
		INSERT INTO errand.errand_demand (id, requester_id, store_id, deadline)
		SELECT id, id, 1, now() + interval '1 day' FROM generate_series(1,4) AS id;
		INSERT INTO errand.errand_demand_item
		(id, errand_demand_id, requester_id, store_id, product_template_id, estimated_unit_price_cents, quantity, service_fee_per_unit_cents)
		SELECT id, id, id, 1, id, 100, 1, 10 FROM generate_series(1,4) AS id;
		INSERT INTO errand.errand_task_assignment (task_id, task_item_id, demand_item_id, purchaser_id, service_fee_per_unit_cents)
		VALUES (10, 101, 1, 1, 10), (10, 101, 2, 2, 10), (10, 101, 3, 3, 10), (10, 101, 4, 4, 10);
	`)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := GetErrandTaskList(ctx, 1, &errandv1.GetErrandTaskListRequest{
		Page:     1,
		PageSize: 10,
		FilterStatuses: []errandv1.ErrandTaskStatus{
			errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_SHOPPING,
			errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_DISTRIBUTING,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalCount != 2 || len(resp.ErrandTasks) != 2 ||
		resp.ErrandTasks[0].TaskId != 20 || resp.ErrandTasks[1].TaskId != 10 {
		t.Fatalf("multi-status tasks = %+v", resp)
	}
	if resp.ErrandTasks[1].ParticipantCount != 4 ||
		!slices.Equal(resp.ErrandTasks[1].ParticipantAvatars, []string{"/a.png", "", "/c.png"}) {
		t.Fatalf(
			"task 10 inline participants = %d %v",
			resp.ErrandTasks[1].ParticipantCount,
			resp.ErrandTasks[1].ParticipantAvatars,
		)
	}

	for _, tc := range []struct {
		name    string
		keyword string
		wantIDs []int64
	}{
		{"task_no", "et-alpha", []int64{10}},
		{"title_snapshot", "matcha", []int64{20}},
		{"literal percent in store name", "%", []int64{20}},
		{"store name matches multiple", "campus", []int64{30, 10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := GetErrandTaskList(ctx, 1, &errandv1.GetErrandTaskListRequest{
				Page:     1,
				PageSize: 10,
				Keyword:  tc.keyword,
			})
			if err != nil {
				t.Fatal(err)
			}
			var ids []int64
			for _, task := range got.ErrandTasks {
				ids = append(ids, task.TaskId)
			}
			if !slices.Equal(ids, tc.wantIDs) || int(got.TotalCount) != len(tc.wantIDs) {
				t.Fatalf("keyword %q got %v total=%d, want %v", tc.keyword, ids, got.TotalCount, tc.wantIDs)
			}
		})
	}
}

func TestGetBuyerOrderBriefListKeywordSearch(t *testing.T) {
	db := testutil.NewPostgres(
		t,
		"002_west_pocket.sql",
		"005_admin_identity.sql",
		"006_admin_catalog_spot.sql",
		"007_admin_corrections.sql",
		"008_admin_errand_pocket.sql",
	)
	previousDB, previousCatalog := postgres.DB, client.CatalogInternalClient
	postgres.DB = db
	client.CatalogInternalClient = buyerOrderSearchCatalogClient{}
	t.Cleanup(func() {
		postgres.DB = previousDB
		client.CatalogInternalClient = previousCatalog
	})
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO catalog.catalog_store (id, name, address, created_by_user_id)
		VALUES (1, 'Campus Bakery', '', 1), (2, 'Milk_Bar 100%', '', 1);
		INSERT INTO catalog.catalog_product_template (id, store_id, title, description, price_cents, created_by_user_id)
		VALUES (11, 1, 'Croissant', 'Butter', 500, 1), (12, 2, 'Oat Latte', 'Iced', 1200, 1);
		INSERT INTO errand.errand_demand (id, requester_id, store_id, deadline, created_at)
		VALUES
			(101, 7, 1, now() + interval '1 day', '2026-10-06 10:00:00Z'),
			(102, 7, 2, now() + interval '1 day', '2026-10-06 11:00:00Z');
		INSERT INTO errand.errand_demand_item
		(errand_demand_id, requester_id, store_id, product_template_id, estimated_unit_price_cents, quantity, service_fee_per_unit_cents)
		VALUES (101, 7, 1, 11, 500, 1, 50), (102, 7, 2, 12, 1200, 1, 100);
	`)
	if err != nil {
		t.Fatal(err)
	}

	results, total, err := GetBuyerOrderBriefList(ctx, 7, nil, nil, "croissant", 1, 10)
	if err != nil || total != 1 || len(results) != 1 || results[0].ErrandDemandID != 101 {
		t.Fatalf("product keyword got %v total=%d err=%v", results, total, err)
	}

	results, total, err = GetBuyerOrderBriefList(ctx, 7, nil, nil, "100%", 2, 10)
	if err != nil || total != 1 || len(results) != 0 {
		t.Fatalf("trailing empty page got len=%d total=%d err=%v, want len=0 total=1", len(results), total, err)
	}
}
