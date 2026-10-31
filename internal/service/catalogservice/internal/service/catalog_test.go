package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// This driver injects relation-write failures without requiring a PostgreSQL
// server, and records whether product writes belong to the same transaction.
type catalogTestDB struct {
	failTable  string
	inTx       bool
	committed  bool
	rolledBack bool
	outsideTx  bool
}

func (d *catalogTestDB) Connect(context.Context) (driver.Conn, error) {
	return &catalogTestConn{db: d}, nil
}
func (d *catalogTestDB) Driver() driver.Driver            { return d }
func (d *catalogTestDB) Open(string) (driver.Conn, error) { return &catalogTestConn{db: d}, nil }

type catalogTestConn struct{ db *catalogTestDB }

func (c *catalogTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *catalogTestConn) Close() error { return nil }
func (c *catalogTestConn) Begin() (driver.Tx, error) {
	c.db.inTx = true
	return c, nil
}

func (c *catalogTestConn) Commit() error {
	c.db.inTx = false
	c.db.committed = true
	return nil
}

func (c *catalogTestConn) Rollback() error {
	c.db.inTx = false
	c.db.rolledBack = true
	return nil
}

func (c *catalogTestConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if strings.HasPrefix(query, "INSERT ") {
		if !c.db.inTx {
			c.db.outsideTx = true
		}
		if c.db.failTable != "" && strings.Contains(query, c.db.failTable) {
			return nil, errors.New("injected relation write failure")
		}
	}
	return &catalogTestRows{}, nil
}

func (c *catalogTestConn) ExecContext(
	ctx context.Context,
	query string,
	args []driver.NamedValue,
) (driver.Result, error) {
	_, err := c.QueryContext(ctx, query, args)
	return driver.RowsAffected(1), err
}

type catalogTestRows struct{ read bool }

func (*catalogTestRows) Columns() []string { return []string{"id"} }
func (*catalogTestRows) Close() error      { return nil }
func (r *catalogTestRows) Next(values []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	values[0] = int64(1)
	return nil
}

func TestCreateProductTemplateIsAtomic(t *testing.T) {
	for _, failTable := range []string{"", "catalog_product_barcode", "catalog_product_image"} {
		t.Run("failure_"+failTable, func(t *testing.T) {
			state := &catalogTestDB{failTable: failTable}
			db := bun.NewDB(sql.OpenDB(state), pgdialect.New())
			previous := postgres.DB
			postgres.DB = db
			t.Cleanup(func() {
				postgres.DB = previous
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			product, err := CreateProductTemplate(
				context.Background(),
				1,
				"Milk",
				"",
				100,
				"https://example.com/image.png",
				"12345",
				1,
			)
			if state.outsideTx {
				t.Error("product data was written outside the transaction")
			}
			if failTable == "" {
				if err != nil || product == nil || !state.committed || state.rolledBack {
					t.Fatalf("successful creation: product=%v err=%v state=%+v", product, err, state)
				}
			} else if err == nil || product != nil || state.committed || !state.rolledBack {
				t.Fatalf("failed creation must roll back: product=%v err=%v state=%+v", product, err, state)
			}
		})
	}
}
