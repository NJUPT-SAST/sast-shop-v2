package v1

import (
	"context"
	"testing"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestCatalogWritesRequireActiveMember(t *testing.T) {
	cases := []struct {
		name string
		user *interceptor.AuthUser
		code connect.Code
	}{
		{"missing_context", nil, connect.CodeUnauthenticated},
		{"zero_id", &interceptor.AuthUser{Role: "admin", Status: "active"}, connect.CodeUnauthenticated},
		{"negative_id", &interceptor.AuthUser{UserID: -1, Role: "user", Status: "active"}, connect.CodeUnauthenticated},
		{"missing_status", &interceptor.AuthUser{UserID: 42, Role: "admin"}, connect.CodeUnauthenticated},
		{
			"restricted",
			&interceptor.AuthUser{UserID: 42, Role: "user", Status: "restricted"},
			connect.CodeUnauthenticated,
		},
		{
			"banned_admin",
			&interceptor.AuthUser{UserID: 42, Role: "admin", Status: "banned"},
			connect.CodeUnauthenticated,
		},
		{
			"deleted_superadmin",
			&interceptor.AuthUser{UserID: 42, Role: "superadmin", Status: "deleted"},
			connect.CodeUnauthenticated,
		},
		{"missing_role", &interceptor.AuthUser{UserID: 42, Status: "active"}, connect.CodePermissionDenied},
		{
			"unknown_role",
			&interceptor.AuthUser{UserID: 42, Role: "guest", Status: "active"},
			connect.CodePermissionDenied,
		},
	}
	calls := []struct {
		name string
		call func(context.Context) error
	}{
		{"create_store", func(ctx context.Context) error {
			_, err := (&CatalogServiceServer{}).CreateStore(ctx, connect.NewRequest(&catalogv1.CreateStoreRequest{}))
			return err
		}},
		{"update_store", func(ctx context.Context) error {
			_, err := (&CatalogServiceServer{}).UpdateStore(ctx, connect.NewRequest(&catalogv1.UpdateStoreRequest{}))
			return err
		}},
		{"create_category", func(ctx context.Context) error {
			_, err := (&ProductTemplateServiceServer{}).CreateProductCategory(
				ctx,
				connect.NewRequest(&catalogv1.CreateProductCategoryRequest{}),
			)
			return err
		}},
		{"delete_category", func(ctx context.Context) error {
			_, err := (&ProductTemplateServiceServer{}).DeleteProductCategory(
				ctx,
				connect.NewRequest(&catalogv1.DeleteProductCategoryRequest{}),
			)
			return err
		}},
		{"create_template", func(ctx context.Context) error {
			_, err := (&ProductTemplateServiceServer{}).CreateProductTemplate(
				ctx,
				connect.NewRequest(&catalogv1.CreateProductTemplateRequest{}),
			)
			return err
		}},
		{"update_template", func(ctx context.Context) error {
			_, err := (&ProductTemplateServiceServer{}).UpdateProductTemplate(
				ctx,
				connect.NewRequest(&catalogv1.UpdateProductTemplateRequest{}),
			)
			return err
		}},
		{"delete_template", func(ctx context.Context) error {
			_, err := (&ProductTemplateServiceServer{}).DeleteProductTemplate(
				ctx,
				connect.NewRequest(&catalogv1.DeleteProductTemplateRequest{}),
			)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.user != nil {
				ctx = interceptor.SetUserToContext(ctx, tc.user)
			}
			for _, call := range calls {
				t.Run(call.name, func(t *testing.T) {
					if err := call.call(ctx); connect.CodeOf(err) != tc.code {
						t.Fatalf("error = %v, want %v", err, tc.code)
					}
				})
			}
		})
	}
}

func TestCatalogWritesAllowMemberRoles(t *testing.T) {
	db := testutil.NewPostgres(t, "006_admin_catalog_spot.sql", "010_store_product_categories.sql")
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = previous })
	for _, role := range []string{"user", "admin", "superadmin"} {
		t.Run(role, func(t *testing.T) {
			ctx := interceptor.SetUserToContext(context.Background(), &interceptor.AuthUser{
				UserID: 42, Role: role, Status: "active",
			})
			stores := &CatalogServiceServer{}
			created, err := stores.CreateStore(ctx, connect.NewRequest(&catalogv1.CreateStoreRequest{
				Name: role + " shop", Address: "Campus",
			}))
			if err != nil {
				t.Fatal(err)
			}
			store := created.Msg.Store
			logo := "https://example.test/" + role + ".png"
			updated, err := stores.UpdateStore(ctx, connect.NewRequest(&catalogv1.UpdateStoreRequest{
				Store:      &catalogv1.Store{Id: store.Id, LogoUrl: logo},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"logo_url"}},
			}))
			if err != nil {
				t.Fatal(err)
			}
			if updated.Msg.Store.LogoUrl != logo || updated.Msg.Store.Name != store.Name {
				t.Fatalf("incorrect store image update: %v", updated.Msg.Store)
			}
			var owner int64
			if err := db.NewSelect().TableExpr("catalog.catalog_store").Column("created_by_user_id").
				Where("id = ?", store.Id).Scan(ctx, &owner); err != nil || owner != 42 {
				t.Fatalf("store creator = %d, error = %v", owner, err)
			}

			checkTemplateWrites(t, ctx, db, store.Id, logo)
		})
	}
}

func checkTemplateWrites(t *testing.T, ctx context.Context, db bun.IDB, storeID int64, logo string) {
	t.Helper()
	member, _ := interceptor.UserFromContext(ctx)
	role := member.Role
	templates := &ProductTemplateServiceServer{}
	createdCategory, err := templates.CreateProductCategory(
		ctx,
		connect.NewRequest(&catalogv1.CreateProductCategoryRequest{StoreId: storeID, Name: "Dairy"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	categoryID := createdCategory.Msg.GetCategory().GetId()
	if categoryID <= 0 {
		t.Fatalf("created category = %v, want a positive ID", createdCategory.Msg.GetCategory())
	}
	product, err := templates.CreateProductTemplate(
		ctx,
		connect.NewRequest(&catalogv1.CreateProductTemplateRequest{
			StoreId: storeID, Title: "Milk", PriceCents: 250, Barcode: "690001", CategoryId: categoryID,
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	pt := product.Msg.ProductTemplate
	if pt.GetCategoryId() != categoryID {
		t.Fatalf("new template category = %d, want %d", pt.GetCategoryId(), categoryID)
	}
	changed, err := templates.UpdateProductTemplate(
		ctx,
		connect.NewRequest(&catalogv1.UpdateProductTemplateRequest{
			ProductTemplate: &catalogv1.ProductTemplate{Id: pt.Id, MainImageUrl: logo, UpdatedAt: pt.UpdatedAt},
			UpdateMask:      &fieldmaskpb.FieldMask{Paths: []string{"main_image_url"}},
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Msg.ProductTemplate.MainImageUrl != logo || changed.Msg.ProductTemplate.Title != "Milk" ||
		changed.Msg.ProductTemplate.GetCategoryId() != categoryID {
		t.Fatalf("incorrect template image update: %v", changed.Msg.ProductTemplate)
	}
	other := interceptor.SetUserToContext(context.Background(), &interceptor.AuthUser{
		UserID: 99, Role: role, Status: "active",
	})
	_, err = templates.DeleteProductTemplate(
		other,
		connect.NewRequest(&catalogv1.DeleteProductTemplateRequest{
			ProductTemplateId: pt.Id,
		}),
	)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("non-owner deletion = %v, want permission_denied", err)
	}
	if _, err := templates.DeleteProductTemplate(
		ctx,
		connect.NewRequest(&catalogv1.DeleteProductTemplateRequest{
			ProductTemplateId: pt.Id,
		}),
	); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.NewSelect().TableExpr("catalog.catalog_product_template").Column("status").
		Where("id = ?", pt.Id).Scan(ctx, &status); err != nil || status != "removed" {
		t.Fatalf("deleted template status = %q, error = %v", status, err)
	}
	if _, err := templates.DeleteProductCategory(
		ctx,
		connect.NewRequest(&catalogv1.DeleteProductCategoryRequest{StoreId: storeID, CategoryId: categoryID}),
	); err != nil {
		t.Fatal(err)
	}
}
