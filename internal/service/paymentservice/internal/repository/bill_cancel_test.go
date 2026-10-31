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

func TestCancelSpotBillSerializesWithConfirmation(t *testing.T) {
	for _, status := range []string{"unpaid", "submitted", "completed", "closed"} {
		t.Run(status, func(t *testing.T) {
			state := &cancelBillTestDB{status: status}
			db := bun.NewDB(sql.OpenDB(state), pgdialect.New())
			previous := postgres.DB
			postgres.DB = db
			t.Cleanup(func() {
				postgres.DB = previous
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			payerID := int64(10)
			_, err := CancelBillBySource(context.Background(), "spot_order", 1, &payerID)
			if !state.locked {
				t.Fatal("cancellation did not lock the bill in its transaction")
			}
			if status == "completed" {
				if !errors.Is(err, ErrBillAlreadyCompleted) || state.updated || state.committed {
					t.Fatalf("completed bill cancellation: err=%v state=%+v", err, state)
				}
			} else if err != nil || !state.updated || !state.committed {
				t.Fatalf("cancellable bill: err=%v state=%+v", err, state)
			}
		})
	}
}

type cancelBillTestDB struct {
	status                           string
	inTx, locked, updated, committed bool
}

func (s *cancelBillTestDB) Connect(context.Context) (driver.Conn, error) { return s, nil }
func (s *cancelBillTestDB) Driver() driver.Driver                        { return s }
func (s *cancelBillTestDB) Open(string) (driver.Conn, error)             { return s, nil }
func (*cancelBillTestDB) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*cancelBillTestDB) Close() error                { return nil }
func (s *cancelBillTestDB) Begin() (driver.Tx, error) { s.inTx = true; return s, nil }
func (s *cancelBillTestDB) Commit() error             { s.inTx = false; s.committed = true; return nil }
func (s *cancelBillTestDB) Rollback() error           { s.inTx = false; return nil }
func (s *cancelBillTestDB) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if !s.inTx || !strings.Contains(query, "FOR UPDATE") {
		return nil, errors.New("bill lookup must lock within the cancellation transaction")
	}
	s.locked = true
	return &cancelBillTestRows{status: s.status}, nil
}

func (s *cancelBillTestDB) ExecContext(_ context.Context, _ string, _ []driver.NamedValue) (driver.Result, error) {
	if !s.inTx || !s.locked {
		return nil, errors.New("bill update must follow the locked status check")
	}
	s.updated = true
	return driver.RowsAffected(1), nil
}

type cancelBillTestRows struct {
	status string
	read   bool
}

func (*cancelBillTestRows) Columns() []string { return []string{"status"} }
func (*cancelBillTestRows) Close() error      { return nil }
func (r *cancelBillTestRows) Next(values []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	values[0] = r.status
	return nil
}
