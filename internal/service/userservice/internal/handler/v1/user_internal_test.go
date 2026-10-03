package v1

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"

	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

func TestDirectorySearchRequiresInternalServiceIdentity(t *testing.T) {
	t.Setenv("WEST_POCKET_INTERNAL_TOKEN", "directory-test-secret-at-least-32-characters")
	server := &UserInternalServer{}
	for _, supplied := range []string{"", "incorrect"} {
		r := connect.NewRequest(&userv1.SearchUsersRequest{Query: "小明"})
		r.Header().Set(interceptor.WestPocketServiceHeader, supplied)
		if _, err := server.SearchUsers(context.Background(), r); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("got %v, want unauthenticated", err)
		}
	}
	r := connect.NewRequest(&userv1.GetUsersRequest{UserIds: []int64{1}})
	r.Header().Set(interceptor.WestPocketServiceHeader, "incorrect")
	if _, err := server.GetUsers(context.Background(), r); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("forged batch request: %v", err)
	}
}

func TestWestPocketDirectoryBatchExcludesInactiveUsers(t *testing.T) {
	const token = "directory-test-secret-at-least-32-characters"
	t.Setenv("WEST_POCKET_INTERNAL_TOKEN", token)
	db := bun.NewDB(sql.OpenDB(directoryUserDB{}), pgdialect.New())
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() {
		postgres.DB = previous
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	r := connect.NewRequest(&userv1.GetUsersRequest{UserIds: []int64{1, 2, 3, 4}})
	r.Header().Set(interceptor.WestPocketServiceHeader, token)
	response, err := (&UserInternalServer{}).GetUsers(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Msg.Users) != 1 || response.Msg.Users[0].Id != 1 {
		t.Fatalf("inactive users leaked into candidates: %+v", response.Msg.Users)
	}
}

type directoryUserDB struct{}

func (d directoryUserDB) Connect(context.Context) (driver.Conn, error) { return d, nil }
func (d directoryUserDB) Driver() driver.Driver                        { return d }
func (d directoryUserDB) Open(string) (driver.Conn, error)             { return d, nil }
func (directoryUserDB) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (directoryUserDB) Close() error              { return nil }
func (directoryUserDB) Begin() (driver.Tx, error) { return nil, errors.New("unexpected transaction") }

func (directoryUserDB) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &directoryUserRows{}, nil
}

type directoryUserRows struct{ index int }

func (*directoryUserRows) Columns() []string { return []string{"id", "display_name", "status"} }
func (*directoryUserRows) Close() error      { return nil }
func (r *directoryUserRows) Next(values []driver.Value) error {
	statuses := []string{"active", "banned", "restricted", "deleted"}
	if r.index >= len(statuses) {
		return io.EOF
	}
	values[0], values[1], values[2] = int64(r.index+1), "同名同学", statuses[r.index]
	r.index++
	return nil
}
