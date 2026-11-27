package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
)

func TestCategoryWritesAndTemplateEditWithPostgres(t *testing.T) {
	db := testutil.NewPostgres(t, "006_admin_catalog_spot.sql", "010_store_product_categories.sql")
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = previous })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO catalog.catalog_store (id, name, address, created_by_user_id) VALUES (1, '分类测试店', '', 1)`,
	); err != nil {
		t.Fatal(err)
	}
	root, err := CreateProductCategory(ctx, 1, nil, "食品")
	if err != nil {
		t.Fatal(err)
	}
	product, err := CreateProductTemplate(ctx, 1, "食品商品", "", 100, "", "10001", 1, root.Id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProductCategory(ctx, 1, &root.Id, "零食"); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("add child to root containing a product: %v", err)
	}
	if err := DeleteProductCategory(ctx, 1, root.Id); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("delete category in use: %v", err)
	}

	assertOrdinaryEditIgnoresCategoryLock(t, ctx, product)

	if err := DeleteProductTemplate(ctx, product.Id, 1); err != nil {
		t.Fatal(err)
	}
	child, err := CreateProductCategory(ctx, 1, &root.Id, "零食")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProductTemplate(
		ctx,
		1,
		"另一个商品",
		"",
		100,
		"",
		"10002",
		1,
		root.Id,
	); connect.CodeOf(
		err,
	) != connect.CodeFailedPrecondition {
		t.Fatalf("assign template to root with child: %v", err)
	}
	if err := DeleteProductCategory(ctx, 1, child.Id); err != nil {
		t.Fatal(err)
	}
	if err := DeleteProductCategory(ctx, 1, root.Id); err != nil {
		t.Fatal(err)
	}
	deleted, err := GetProductTemplate(ctx, product.Id)
	if err != nil || deleted.CategoryId != nil || deleted.PriceCents != 200 {
		t.Fatalf("deleted template history should survive without category: product=%v, err=%v", deleted, err)
	}
}

func assertOrdinaryEditIgnoresCategoryLock(t *testing.T, ctx context.Context, product *catalogv1.ProductTemplate) {
	t.Helper()
	tx, err := postgres.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error(err)
		}
	}()
	if err := lockProductCategoryStore(ctx, tx, 1); err != nil {
		t.Fatal(err)
	}
	editCtx, stopEdit := context.WithTimeout(ctx, 2*time.Second)
	defer stopEdit()
	if _, err := UpdateProductTemplate(editCtx, &catalogv1.ProductTemplate{
		Id: product.Id, UpdatedAt: product.UpdatedAt, PriceCents: 200,
	}, []string{"price_cents"}); err != nil {
		t.Fatalf("normal template edit should complete while category lock is held: %v", err)
	}
	var gotLock bool
	if err := postgres.DB.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended(?, 0))", "catalog.product_category.store:1").
		Scan(&gotLock); err != nil ||
		gotLock {
		t.Fatalf("second transaction unexpectedly obtained category lock: got=%v, err=%v", gotLock, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestFirstChildAndDirectTemplateCannotBothCommit(t *testing.T) {
	db := testutil.NewPostgres(t, "006_admin_catalog_spot.sql", "010_store_product_categories.sql")
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = previous })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO catalog.catalog_store (id, name, address, created_by_user_id) VALUES (1, '并发分类测试店', '', 1)`,
	); err != nil {
		t.Fatal(err)
	}
	root, err := CreateProductCategory(ctx, 1, nil, "食品")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	result := make(chan error, 2)
	go func() {
		<-start
		_, err := CreateProductCategory(ctx, 1, &root.Id, "饮料")
		result <- err
	}()
	go func() {
		<-start
		_, err := CreateProductTemplate(ctx, 1, "食品商品", "", 100, "", "10001", 1, root.Id)
		result <- err
	}()
	close(start)
	good, conflict := 0, 0
	for range 2 {
		if err := <-result; err == nil {
			good++
		} else if connect.CodeOf(err) == connect.CodeFailedPrecondition {
			conflict++
		} else {
			t.Errorf("unexpected concurrent write error: %v", err)
		}
	}
	if good != 1 || conflict != 1 {
		t.Fatalf("got %d successful writes and %d conflicts, want one each", good, conflict)
	}
}
