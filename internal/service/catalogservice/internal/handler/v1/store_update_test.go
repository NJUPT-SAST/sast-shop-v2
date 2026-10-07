package v1

import (
	"context"
	"testing"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestStoreUpdateAllowsAuthenticatedUserAndAdmin(t *testing.T) {
	db := testutil.NewPostgres(t)
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = previous })
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
    INSERT INTO catalog.catalog_store (id, name, address, logo_url, created_by_user_id)
    VALUES (1, 'Original Shop', 'Original Address', 'https://example.test/original.png', 99);
  `); err != nil {
		t.Fatal(err)
	}
	handler := &CatalogServiceServer{}
	for _, role := range []string{"user", "admin"} {
		t.Run(role, func(t *testing.T) {
			authenticated := interceptor.SetUserToContext(
				ctx,
				&interceptor.AuthUser{UserID: 42, Role: role, Status: "active"},
			)
			response, err := handler.UpdateStore(authenticated, connect.NewRequest(&catalogv1.UpdateStoreRequest{
				Store:      &catalogv1.Store{Id: 1, Name: role + " updated", LogoUrl: ""},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "logo_url"}},
			}))
			if err != nil {
				t.Fatal(err)
			}
			if response.Msg.Store.Name != role+" updated" || response.Msg.Store.Address != "Original Address" ||
				response.Msg.Store.LogoUrl != "" {
				t.Fatalf("partial update did not preserve unselected fields: %v", response.Msg.Store)
			}
			var savedName string
			if err := db.NewSelect().
				TableExpr("catalog.catalog_store").
				Column("name").
				Where("id = 1").
				Scan(ctx, &savedName); err != nil {
				t.Fatal(err)
			}
			if savedName != role+" updated" {
				t.Fatalf("stored name %q does not match update", savedName)
			}
		})
	}
}

func TestStoreUpdateRequiresAuthenticatedContext(t *testing.T) {
	_, err := (&CatalogServiceServer{}).UpdateStore(
		context.Background(),
		connect.NewRequest(&catalogv1.UpdateStoreRequest{
			Store:      &catalogv1.Store{Id: 1, Name: "updated"},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		}),
	)
	if err == nil {
		t.Fatal("store update without authenticated context must fail")
	}
}
