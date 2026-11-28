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

func TestGetStoreRequiresIdentityAndValidID(t *testing.T) {
	handler := &CatalogServiceServer{}
	_, err := handler.GetStore(
		context.Background(),
		connect.NewRequest(&catalogv1.CatalogServiceGetStoreRequest{StoreId: 1}),
	)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("missing identity returned %v, want unauthenticated", err)
	}
	authenticated := interceptor.SetUserToContext(context.Background(), &interceptor.AuthUser{UserID: 42})
	for _, id := range []int64{0, -1} {
		_, err := handler.GetStore(
			authenticated,
			connect.NewRequest(&catalogv1.CatalogServiceGetStoreRequest{StoreId: id}),
		)
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("invalid store_id %d returned %v, want invalid_argument", id, err)
		}
	}
}

func TestGetStoreReturnsVisibleStoreAndHidesRemovedOrBlocked(t *testing.T) {
	db := testutil.NewPostgres(t, "006_admin_catalog_spot.sql")
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = previous })
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO catalog.catalog_store (id, name, address, logo_url, theme_color, status, moderation_status, created_by_user_id)
		VALUES
			(1, 'Campus Shop', 'Dorm 1', 'https://example.test/logo.png', '#FF5500', 'active', 'allowed', 1),
			(2, 'Removed Shop', 'Dorm 2', '', '#FFFFFF', 'removed', 'allowed', 1),
			(3, 'Blocked Shop', 'Dorm 3', '', '#FFFFFF', 'active', 'blocked', 1);
	`); err != nil {
		t.Fatal(err)
	}

	handler := &CatalogServiceServer{}
	authenticated := interceptor.SetUserToContext(
		ctx,
		&interceptor.AuthUser{UserID: 42, Role: "user", Status: "active"},
	)

	resp, err := handler.GetStore(
		authenticated,
		connect.NewRequest(&catalogv1.CatalogServiceGetStoreRequest{StoreId: 1}),
	)
	if err != nil {
		t.Fatal(err)
	}
	store := resp.Msg.GetStore()
	if store.GetId() != 1 || store.GetName() != "Campus Shop" || store.GetAddress() != "Dorm 1" ||
		store.GetLogoUrl() != "https://example.test/logo.png" || store.GetThemeColor() != "#FF5500" {
		t.Fatalf("unexpected store response: %v", store)
	}

	for _, id := range []int64{2, 3, 999} {
		_, err := handler.GetStore(
			authenticated,
			connect.NewRequest(&catalogv1.CatalogServiceGetStoreRequest{StoreId: id}),
		)
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("store_id %d returned %v, want not_found", id, err)
		}
	}
}
