package v1

import (
	"context"
	"testing"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
)

func TestGetProductTemplateReturnsCurrentDetails(t *testing.T) {
	db := testutil.NewPostgres(t)
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = previous })
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
    INSERT INTO catalog.catalog_store (id, name, address, created_by_user_id)
    VALUES (1, 'Campus Shop', '', 1);
    INSERT INTO catalog.catalog_product_template
    (id, store_id, title, description, price_cents, created_by_user_id, updated_at)
    VALUES (10, 1, 'Water', '550ml', 200, 1, '2026-10-07 01:00:00.123456+00');
    INSERT INTO catalog.catalog_product_barcode (product_template_id, barcode) VALUES (10, '690000000001');
    INSERT INTO catalog.catalog_product_image (product_template_id, image_url)
    VALUES (10, 'https://example.test/water.png');
  `); err != nil {
		t.Fatal(err)
	}
	handler := &ProductTemplateServiceServer{}
	for _, role := range []string{"user", "admin"} {
		t.Run(role, func(t *testing.T) {
			authenticated := interceptor.SetUserToContext(
				ctx,
				&interceptor.AuthUser{UserID: 42, Role: role, Status: "active"},
			)
			response, err := handler.GetProductTemplate(
				authenticated,
				connect.NewRequest(&catalogv1.ProductTemplateServiceGetProductTemplateRequest{ProductTemplateId: 10}),
			)
			if err != nil {
				t.Fatal(err)
			}
			template := response.Msg.ProductTemplate
			if template.GetId() != 10 || template.GetTitle() != "Water" || template.GetStoreId() != 1 ||
				template.GetDescription() != "550ml" || template.GetPriceCents() != 200 ||
				template.GetBarcode() != "690000000001" || template.GetMainImageUrl() != "https://example.test/water.png" ||
				template.GetUpdatedAt().GetNanos() != 123456000 {
				t.Fatalf("incomplete or stale product template: %v", template)
			}
		})
	}
	authenticated := interceptor.SetUserToContext(
		ctx,
		&interceptor.AuthUser{UserID: 42, Role: "user", Status: "active"},
	)
	_, err := handler.GetProductTemplate(
		authenticated,
		connect.NewRequest(&catalogv1.ProductTemplateServiceGetProductTemplateRequest{ProductTemplateId: 999}),
	)
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("missing template returned %v, want not_found", err)
	}
}

func TestGetProductTemplateRequiresIdentityAndValidID(t *testing.T) {
	handler := &ProductTemplateServiceServer{}
	_, err := handler.GetProductTemplate(
		context.Background(),
		connect.NewRequest(&catalogv1.ProductTemplateServiceGetProductTemplateRequest{ProductTemplateId: 10}),
	)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("missing identity returned %v", err)
	}
	authenticated := interceptor.SetUserToContext(context.Background(), &interceptor.AuthUser{UserID: 42})
	for _, id := range []int64{0, -1} {
		_, err := handler.GetProductTemplate(
			authenticated,
			connect.NewRequest(&catalogv1.ProductTemplateServiceGetProductTemplateRequest{ProductTemplateId: id}),
		)
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("invalid ID %d returned %v", id, err)
		}
	}
}
