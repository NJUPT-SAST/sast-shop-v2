package service

import (
	"context"
	"testing"

	adminv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/repository"
	"github.com/uptrace/bun"
)

func setupAdminSpot(t *testing.T) (context.Context, *bun.DB) {
	t.Helper()
	db := testutil.NewPostgres(
		t,
		"002_west_pocket.sql",
		"005_admin_identity.sql",
		"006_admin_catalog_spot.sql",
		"007_admin_corrections.sql",
		"009_spot_admin_inventory.sql",
	)
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = previous })
	ctx := context.Background()
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO "user".user_account (id,feishu_open_id,role,display_name) VALUES (42,'admin-42','admin','Operator');
    INSERT INTO catalog.catalog_store (id,name,address,created_by_user_id) VALUES (100,'Store','',7);
    INSERT INTO catalog.catalog_product_template (id,title,price_cents,store_id,created_by_user_id) VALUES (200,'Milk',300,100,7);
    INSERT INTO spot.spot_goods (id,seller_id,store_id,product_template_id,sale_price_cents,stock_total) VALUES (300,7,100,200,300,5);
    INSERT INTO spot.spot_order (id,order_no,purchaser_id,listing_id,product_template_id,title_snapshot,quantity,unit_price_cents,total_amount_cents) VALUES (400,'SO400',8,300,200,'Milk',1,300,300)`,
	); err != nil {
		t.Fatal(err)
	}
	return interceptor.SetUserToContext(
		ctx,
		&interceptor.AuthUser{UserID: 42, Role: "admin", Status: "active", AuthVersion: 1},
	), db
}

func TestAdminSpotModerationCannotBeBypassedAndPreservesInventory(t *testing.T) {
	ctx, db := setupAdminSpot(t)
	initial, err := GetAdminRecord(
		ctx,
		&adminv1.GetRecordRequest{Resource: adminv1.ResourceKind_RESOURCE_KIND_SPOT_GOODS, Id: "300"},
	)
	if err != nil {
		t.Fatal(err)
	}
	req := &adminv1.MutateRecordRequest{
		Resource:          adminv1.ResourceKind_RESOURCE_KIND_SPOT_GOODS,
		Id:                "300",
		Action:            "block",
		ExpectedUpdatedAt: initial.Record.UpdatedAt,
		ExpectedRound:     1,
		RequestId:         "block-listing",
		Reason:            "核实商品",
		Evidence:          "已核实原始信息",
	}
	blocked, err := MutateAdminRecord(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []func() error{
		func() error { return UpdateSpotGoodsStock(ctx, 7, 300, 10, blocked.Record.UpdatedAt) },
		func() error { return UpdateSpotGoodsPrice(ctx, 7, 300, 200, blocked.Record.UpdatedAt) },
		func() error {
			_, err := CreateSpotOrders(ctx, 8, &spotv1.CreateSpotOrdersRequest{SpotOrders: []*spotv1.CreateSpotOrder{{SpotListingId: 300, Quantity: 1, UpdatedAt: blocked.Record.UpdatedAt}}})
			return err
		},
	} {
		if err := call(); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("governance bypass: %v", err)
		}
	}
	items, err := repository.ListSpotGoods(ctx, 0, 0, 20, "")
	if err != nil || len(items) != 0 {
		t.Fatalf("blocked public list: %v %v", items, err)
	}
	saved, err := repository.GetSpotGoodsByID(ctx, 300)
	if err != nil || saved.StockTotal != 5 || saved.ClosedAt != nil {
		t.Fatalf("governance changed stock/close state: %v %v", saved, err)
	}
	_, err = MutateAdminRecord(ctx, req)
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	count, err := db.NewSelect().TableExpr("spot.admin_action_log").Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("audit count=%d err=%v", count, err)
	}
	req.Action = "unblock"
	req.RequestId = "restore-listing"
	req.ExpectedUpdatedAt = blocked.Record.UpdatedAt
	restored, err := MutateAdminRecord(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateSpotGoodsStock(ctx, 7, 300, 6, restored.Record.UpdatedAt); err != nil {
		t.Fatalf("restored listing not editable: %v", err)
	}
}

func TestAdminSpotListsAllOrdersWithoutImpersonation(t *testing.T) {
	ctx, _ := setupAdminSpot(t)
	response, err := ListAdminRecords(
		ctx,
		&adminv1.ListRecordsRequest{Resource: adminv1.ResourceKind_RESOURCE_KIND_SPOT_ORDER, UserId: "7"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.TotalCount != 1 || len(response.Records) != 1 || response.Records[0].Id != "400" {
		t.Fatalf("global orders: %v", response)
	}
	response, err = ListAdminRecords(
		ctx,
		&adminv1.ListRecordsRequest{Resource: adminv1.ResourceKind_RESOURCE_KIND_SPOT_ORDER, Keyword: "milk"},
	)
	if err != nil || response.TotalCount != 1 {
		t.Fatalf("order search: %v %v", response, err)
	}
	ordinary := interceptor.SetUserToContext(ctx, &interceptor.AuthUser{UserID: 7, Role: "user", Status: "active"})
	if _, err := GetAdminRecord(
		ordinary,
		&adminv1.GetRecordRequest{Resource: adminv1.ResourceKind_RESOURCE_KIND_SPOT_ORDER, Id: "400"},
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("ordinary admin read: %v", err)
	}
}

func TestCatalogModerationBlocksSpotPurchaseAndPublication(t *testing.T) {
	ctx, db := setupAdminSpot(t)
	for _, table := range []string{"catalog.catalog_store", "catalog.catalog_product_template"} {
		if _, err := db.ExecContext(ctx, "UPDATE "+table+" SET moderation_status='blocked'"); err != nil {
			t.Fatal(err)
		}
		err := db.RunInTx(
			ctx,
			nil,
			func(ctx context.Context, tx bun.Tx) error { return repository.LockCatalogForSale(ctx, tx, 100, 200) },
		)
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("blocked %s is tradable: %v", table, err)
		}
		items, err := repository.ListSpotGoods(ctx, 0, 0, 20, "")
		if err != nil || len(items) != 0 {
			t.Fatalf("blocked catalog appears: %v %v", items, err)
		}
		goods, err := repository.GetSpotGoodsByID(ctx, 300)
		if err != nil {
			t.Fatal(err)
		}
		if err := CreateSpotGoodsTx(ctx, goods); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("republish blocked catalog: %v", err)
		}
		if _, err := db.ExecContext(ctx, "UPDATE "+table+" SET moderation_status='allowed'"); err != nil {
			t.Fatal(err)
		}
	}
}
