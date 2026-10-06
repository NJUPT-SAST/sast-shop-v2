package testutil

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

func NewPostgres(t *testing.T) *bun.DB {
	t.Helper()
	dsn := os.Getenv("SAST_SHOP_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set SAST_SHOP_TEST_POSTGRES_URL to run isolated PostgreSQL integration tests")
	}
	base, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("invalid test PostgreSQL URL")
	}
	admin := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn))), pgdialect.New())
	name := "sast_shop_test_" + rand.Text()
	ctx := context.Background()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE ?", bun.Ident(name)); err != nil {
		if closeErr := admin.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatalf("create isolated test database: %v", err)
	}
	base.Path = "/" + name
	db := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(base.String()))), pgdialect.New())
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
		if _, err := admin.ExecContext(ctx, "DROP DATABASE ? WITH (FORCE)", bun.Ident(name)); err != nil {
			t.Errorf("drop isolated test database: %v", err)
		}
		if err := admin.Close(); err != nil {
			t.Error(err)
		}
	})
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate database migration")
	}
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../migrations/001_init.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatalf("apply migration to isolated test database: %v", err)
	}
	return db
}
