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

func TestWestPocketCancellationLocksSourceAndRejectsAnyStartedPayment(t *testing.T) {
	for _, test := range []struct {
		name     string
		statuses []string
		blocked  bool
	}{
		{"unpaid and closed", []string{"unpaid", "closed", "unpaid"}, false},
		{"one submitted", []string{"unpaid", "submitted", "unpaid"}, true},
		{"one completed", []string{"unpaid", "completed"}, true},
		{"none created yet", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &westPocketTestDB{statuses: test.statuses}
			installWestPocketTestDB(t, state)
			err := CancelWestPocketBillsIfUnpaid(context.Background(), 42, func(context.Context) error {
				if !state.sourceLocked || !state.inTx || state.rowsLocked {
					t.Fatal("state must be authorized after source lock and before bill locking")
				}
				state.authorized = true
				return nil
			})
			if test.blocked {
				if !errors.Is(err, ErrWestPocketPaymentStarted) || state.updated || state.committed {
					t.Fatalf("started payment partially cancelled: err=%v state=%+v", err, state)
				}
			} else if err != nil || !state.updated || !state.committed {
				t.Fatalf("unpaid cancellation failed: err=%v state=%+v", err, state)
			}
		})
	}
}

func TestWestPocketLatePublisherChecksStateInsideSourceLock(t *testing.T) {
	state := &westPocketTestDB{}
	installWestPocketTestDB(t, state)
	late := errors.New("activity already cancelled")
	err := WithWestPocketSourceLock(context.Background(), 42, func(context.Context, bun.Tx) error {
		if !state.sourceLocked || !state.inTx {
			t.Fatal("late publisher checked state before source serialization")
		}
		return late
	})
	if !errors.Is(err, late) || state.committed || state.updated {
		t.Fatalf("failed authorization committed: err=%v state=%+v", err, state)
	}
}

func TestWestPocketIdempotencyLookupIncludesClosedBills(t *testing.T) {
	state := &westPocketTestDB{statuses: []string{"closed"}}
	db := installWestPocketTestDB(t, state)
	bill, err := GetWestPocketBill(context.Background(), db, 42, 10)
	if err != nil || bill == nil || string(bill.Status) != "closed" {
		t.Fatalf("closed bill disappeared from idempotency lookup: bill=%+v err=%v", bill, err)
	}
	if strings.Contains(state.lastQuery, "status !=") || strings.Contains(state.lastQuery, "status <>") {
		t.Fatal("lookup excludes closed bills")
	}
}

func installWestPocketTestDB(t *testing.T, state *westPocketTestDB) *bun.DB {
	t.Helper()
	db := bun.NewDB(sql.OpenDB(state), pgdialect.New())
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() {
		postgres.DB = previous
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

type westPocketTestDB struct {
	statuses                                                       []string
	lastQuery                                                      string
	inTx, sourceLocked, authorized, rowsLocked, updated, committed bool
}

func (s *westPocketTestDB) Connect(context.Context) (driver.Conn, error) { return s, nil }
func (s *westPocketTestDB) Driver() driver.Driver                        { return s }
func (s *westPocketTestDB) Open(string) (driver.Conn, error)             { return s, nil }
func (*westPocketTestDB) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*westPocketTestDB) Close() error                { return nil }
func (s *westPocketTestDB) Begin() (driver.Tx, error) { s.inTx = true; return s, nil }
func (s *westPocketTestDB) Commit() error             { s.inTx = false; s.committed = true; return nil }
func (s *westPocketTestDB) Rollback() error           { s.inTx = false; return nil }
func (s *westPocketTestDB) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	s.lastQuery = query
	if strings.Contains(query, "FOR UPDATE") {
		if !s.sourceLocked || !s.authorized || !s.inTx {
			return nil, errors.New("row lock precedes source lock/authorization")
		}
		s.rowsLocked = true
	}
	return &westPocketStatusRows{statuses: s.statuses}, nil
}

func (s *westPocketTestDB) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if strings.Contains(query, "pg_advisory_xact_lock") {
		if !s.inTx || !strings.Contains(query, "west_pocket:42") {
			return nil, errors.New("source lock must identify activity inside transaction")
		}
		s.sourceLocked = true
		return driver.RowsAffected(1), nil
	}
	if !s.inTx || !s.sourceLocked || !s.rowsLocked || !s.authorized {
		return nil, errors.New("update precedes complete locked validation")
	}
	s.updated = true
	return driver.RowsAffected(int64(len(s.statuses))), nil
}

type westPocketStatusRows struct {
	statuses []string
	index    int
}

func (*westPocketStatusRows) Columns() []string { return []string{"status"} }
func (*westPocketStatusRows) Close() error      { return nil }
func (r *westPocketStatusRows) Next(values []driver.Value) error {
	if r.index >= len(r.statuses) {
		return io.EOF
	}
	values[0] = r.statuses[r.index]
	r.index++
	return nil
}
