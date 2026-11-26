package service

import (
	"context"
	"testing"

	adminv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
	"github.com/uptrace/bun"
)

func setupAdminCatalog(t *testing.T) (context.Context, *bun.DB) {
	t.Helper()
	db := testutil.NewPostgres(
		t,
		"005_admin_identity.sql",
		"006_admin_catalog_spot.sql",
		"010_store_product_categories.sql",
	)
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = previous })
	ctx := context.Background()
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO "user".user_account (id,feishu_open_id,role,display_name) VALUES (42,'admin-42','admin','Operator');
    INSERT INTO catalog.catalog_store (id,name,address,created_by_user_id) VALUES (100,'Shared shop','Campus',7);
    INSERT INTO catalog.catalog_product_category (id,store_id,name) VALUES (1,100,'Food');
    INSERT INTO catalog.catalog_product_template (id,title,description,price_cents,store_id,created_by_user_id,category_id) VALUES (200,'Milk','Original',250,100,7,1);
    INSERT INTO catalog.catalog_product_barcode (product_template_id,barcode) VALUES (200,'690001')`,
	); err != nil {
		t.Fatal(err)
	}
	return interceptor.SetUserToContext(
		ctx,
		&interceptor.AuthUser{UserID: 42, Role: "admin", Status: "active", AuthVersion: 1},
	), db
}

func TestAdminCatalogMutationIsAuditedIdempotentAndVersioned(t *testing.T) {
	ctx, db := setupAdminCatalog(t)
	initial, err := GetAdminRecord(
		ctx,
		&adminv1.GetRecordRequest{Resource: adminv1.ResourceKind_RESOURCE_KIND_PRODUCT, Id: "200"},
	)
	if err != nil {
		t.Fatal(err)
	}
	req := &adminv1.MutateRecordRequest{
		Resource:          adminv1.ResourceKind_RESOURCE_KIND_PRODUCT,
		Id:                "200",
		Action:            "update",
		ExpectedUpdatedAt: initial.Record.UpdatedAt,
		ExpectedRound:     1,
		RequestId:         "catalog-edit-1",
		Reason:            "核对商品信息",
		Evidence:          "已核实原始信息",
		Values:            map[string]string{"title": "Corrected Milk", "price_cents": "300"},
	}
	first, err := MutateAdminRecord(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MutateAdminRecord(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Record.Title != "Corrected Milk" || first.Record.GetAmountCents() != 300 ||
		!first.Record.UpdatedAt.AsTime().Equal(second.Record.UpdatedAt.AsTime()) {
		t.Fatalf("incorrect mutation or replay: %v %v", first, second)
	}
	audit, err := admin.ListAudit(
		ctx,
		db,
		"catalog",
		&adminv1.ListAuditRequest{
			Resource:   adminv1.ResourceKind_RESOURCE_KIND_PRODUCT,
			ResourceId: "200",
			RequestId:  req.RequestId,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if audit.TotalCount != 1 || len(audit.Entries) != 1 {
		t.Fatalf("audit = %v", audit)
	}
	assertCatalogAuditPrice(t, audit)
	req.RequestId = "catalog-edit-stale"
	if _, err := MutateAdminRecord(ctx, req); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale write: %v", err)
	}
	req.RequestId = "catalog-edit-1"
	req.Values["title"] = "Different"
	if _, err := MutateAdminRecord(ctx, req); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("changed replay: %v", err)
	}
}

func TestCatalogModerationPreservesSharedEditsAndBlocksForbiddenProducts(t *testing.T) {
	ctx, _ := setupAdminCatalog(t)
	initial, err := GetAdminRecord(
		ctx,
		&adminv1.GetRecordRequest{Resource: adminv1.ResourceKind_RESOURCE_KIND_PRODUCT, Id: "200"},
	)
	if err != nil {
		t.Fatal(err)
	}
	req := &adminv1.MutateRecordRequest{
		Resource:          adminv1.ResourceKind_RESOURCE_KIND_PRODUCT,
		Id:                "200",
		Action:            "block",
		ExpectedUpdatedAt: initial.Record.UpdatedAt,
		ExpectedRound:     1,
		RequestId:         "block-product",
		Reason:            "暂停销售核实",
		Evidence:          "已核实原始信息",
	}
	blocked, err := MutateAdminRecord(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	products, total, err := GetProductTemplateList(ctx, 100, 1, 20, "", false,
		catalogv1.ProductTemplateCategoryFilterMode_PRODUCT_TEMPLATE_CATEGORY_FILTER_MODE_UNSPECIFIED, 0)
	if err != nil || total != 0 || len(products) != 0 {
		t.Fatalf("blocked product remains visible: %v %d %v", products, total, err)
	}
	_, err = UpdateProductTemplate(
		ctx,
		&catalogv1.ProductTemplate{Id: 200, Title: "Bypass", UpdatedAt: blocked.Record.UpdatedAt},
		[]string{"title"},
	)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("blocked product editable: %v", err)
	}
	if err := DeleteProductTemplate(ctx, 200, 7); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("blocked product deletable: %v", err)
	}
	req.Action = "unblock"
	req.RequestId = "restore-product"
	req.ExpectedUpdatedAt = blocked.Record.UpdatedAt
	restored, err := MutateAdminRecord(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	_, err = UpdateProductTemplate(
		ctx,
		&catalogv1.ProductTemplate{Id: 200, Title: "Community edit", UpdatedAt: restored.Record.UpdatedAt},
		[]string{"title"},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertCatalogStoreModeration(t, ctx)
}

func TestAdminCatalogRejectsRegularUsersAndRevokedSessions(t *testing.T) {
	ctx, db := setupAdminCatalog(t)
	ordinary := interceptor.SetUserToContext(ctx, &interceptor.AuthUser{UserID: 7, Role: "user", Status: "active"})
	if _, err := ListAdminRecords(
		ordinary,
		&adminv1.ListRecordsRequest{Resource: adminv1.ResourceKind_RESOURCE_KIND_STORE},
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("ordinary access: %v", err)
	}
	initial, err := GetAdminRecord(
		ctx,
		&adminv1.GetRecordRequest{Resource: adminv1.ResourceKind_RESOURCE_KIND_STORE, Id: "100"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(
		ctx,
		`UPDATE "user".user_account SET role='user',auth_version=auth_version+1 WHERE id=42`,
	); err != nil {
		t.Fatal(err)
	}
	_, err = MutateAdminRecord(
		ctx,
		&adminv1.MutateRecordRequest{
			Resource:          adminv1.ResourceKind_RESOURCE_KIND_STORE,
			Id:                "100",
			Action:            "block",
			ExpectedUpdatedAt: initial.Record.UpdatedAt,
			ExpectedRound:     1,
			RequestId:         "revoked",
			Reason:            "test",
			Evidence:          "已核实原始信息",
		},
	)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("revoked session wrote: %v", err)
	}
}

func TestAdminCatalogRejectsUnknownFields(t *testing.T) {
	for _, values := range []map[string]string{{"role": "superadmin"}, {"price_cents": "1.5"}, {"store_id": "0"}, {"main_image_url": "javascript:alert(1)"}} {
		_, err := validateAdminCatalogValues(
			&adminv1.MutateRecordRequest{
				Resource: adminv1.ResourceKind_RESOURCE_KIND_PRODUCT,
				Action:   "update",
				Values:   values,
			},
		)
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("accepted values %v: %v", values, err)
		}
	}
}

func assertCatalogAuditPrice(t *testing.T, audit *adminv1.ListAuditResponse) {
	t.Helper()
	found := false
	for _, f := range audit.Entries[0].After {
		if f.Key == "price_cents" && f.Value == "300" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing edited field in audit: %v", audit)
	}
}

func assertCatalogStoreModeration(t *testing.T, ctx context.Context) {
	t.Helper()
	store, err := GetAdminRecord(
		ctx,
		&adminv1.GetRecordRequest{Resource: adminv1.ResourceKind_RESOURCE_KIND_STORE, Id: "100"},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = MutateAdminRecord(
		ctx,
		&adminv1.MutateRecordRequest{
			Resource:          adminv1.ResourceKind_RESOURCE_KIND_STORE,
			Id:                "100",
			Action:            "block",
			ExpectedUpdatedAt: store.Record.UpdatedAt,
			ExpectedRound:     1,
			RequestId:         "block-store",
			Reason:            "店铺暂停运营",
			Evidence:          "已核实原始信息",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	stores, err := GetStoreList(ctx)
	if err != nil || len(stores) != 0 {
		t.Fatalf("blocked store visible: %v %v", stores, err)
	}
	_, err = CreateProductTemplate(ctx, 100, "New", "", 100, "", "123", 7, 1)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("new template in blocked store: %v", err)
	}
	_, total, err := GetProductTemplateList(ctx, 100, 1, 20, "", false,
		catalogv1.ProductTemplateCategoryFilterMode_PRODUCT_TEMPLATE_CATEGORY_FILTER_MODE_UNSPECIFIED, 0)
	if err != nil || total != 0 {
		t.Fatalf("blocked store templates visible: %d %v", total, err)
	}
}
