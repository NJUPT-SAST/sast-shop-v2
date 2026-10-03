package repository

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

func TestSearchUsersIsActiveBoundedAndTreatsWildcardsLiterally(t *testing.T) {
	state := &searchTestDB{}
	db := bun.NewDB(sql.OpenDB(state), pgdialect.New())
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() {
		postgres.DB = previous
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := SearchActiveUsers(context.Background(), "a%' OR true --_!", 42, 21); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ua.status = 'active'", "ua.id > 42", "ORDER BY ua.id ASC", "LIMIT 21",
		"ILIKE '%a!%'' OR true --!_!!%' ESCAPE '!'",
	} {
		if !strings.Contains(state.query, want) {
			t.Fatalf("query missing %q: %s", want, state.query)
		}
	}
}

type searchTestDB struct{ query string }

func (s *searchTestDB) Connect(context.Context) (driver.Conn, error) { return s, nil }
func (s *searchTestDB) Driver() driver.Driver                        { return s }
func (s *searchTestDB) Open(string) (driver.Conn, error)             { return s, nil }
func (*searchTestDB) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*searchTestDB) Close() error              { return nil }
func (*searchTestDB) Begin() (driver.Tx, error) { return nil, errors.New("unexpected transaction") }
func (s *searchTestDB) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	s.query = query
	return emptyUserRows{}, nil
}

type emptyUserRows struct{}

func (emptyUserRows) Columns() []string         { return []string{"id"} }
func (emptyUserRows) Close() error              { return nil }
func (emptyUserRows) Next([]driver.Value) error { return io.EOF }
