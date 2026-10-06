package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

func TestBillOperationsRequireAuthentication(t *testing.T) {
	operations := billOperations()
	operations["create"] = func(ctx context.Context) error {
		_, err := CreateBill(ctx, 10, 20, 100, nil, nil)
		return err
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			if err := operation(context.Background()); connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatalf("error = %v, want unauthenticated", err)
			}
		})
	}
}

func TestBillOperationsRejectOtherUsers(t *testing.T) {
	db := bun.NewDB(sql.OpenDB(billTestConnector{}), pgdialect.New())
	previousDB := postgres.DB
	postgres.DB = db
	t.Cleanup(func() {
		postgres.DB = previousDB
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})

	for name, operation := range billOperations() {
		t.Run(name, func(t *testing.T) {
			ctx := interceptor.SetUserToContext(context.Background(), &interceptor.AuthUser{UserID: 30})
			if err := operation(ctx); !errors.Is(err, ErrBillPermissionDenied) {
				t.Fatalf("error = %v, want permission denied", err)
			}
		})
	}
	// A payer cannot confirm their own payment, and a payee cannot submit it.
	for _, test := range []struct {
		operation string
		userID    int64
	}{{"confirm", 10}, {"pay", 20}, {"serial number", 20}} {
		t.Run(test.operation+" wrong participant", func(t *testing.T) {
			ctx := interceptor.SetUserToContext(context.Background(), &interceptor.AuthUser{UserID: test.userID})
			if err := billOperations()[test.operation](ctx); !errors.Is(err, ErrBillPermissionDenied) {
				t.Fatalf("error = %v, want permission denied", err)
			}
		})
	}
}

func TestCreateBillRejectsOtherParticipants(t *testing.T) {
	ctx := interceptor.SetUserToContext(context.Background(), &interceptor.AuthUser{UserID: 30})
	if _, err := CreateBill(ctx, 10, 20, 100, nil, nil); !errors.Is(err, ErrBillPermissionDenied) {
		t.Fatalf("error = %v, want permission denied", err)
	}
}

func TestCreateBillRejectsInvalidRequest(t *testing.T) {
	ctx := interceptor.SetUserToContext(context.Background(), &interceptor.AuthUser{UserID: 10})
	sourceType := "spot_order"
	sourceID := int64(1)
	for _, test := range []struct {
		name       string
		payeeID    int64
		amount     int32
		sourceType *string
		sourceID   *int64
		want       error
	}{
		{"invalid participant", 0, 100, nil, nil, ErrInvalidBillRequest},
		{"negative amount", 20, -1, nil, nil, ErrInvalidBillRequest},
		{"source type only", 20, 100, &sourceType, nil, ErrInvalidBillRequest},
		{"source id only", 20, 100, nil, &sourceID, ErrInvalidBillRequest},
		{"self payment", 10, 100, nil, nil, ErrSelfPayment},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := CreateBill(ctx, 10, test.payeeID, test.amount, test.sourceType, test.sourceID)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func billOperations() map[string]func(context.Context) error {
	return map[string]func(context.Context) error{
		"get": func(ctx context.Context) error {
			_, err := GetBill(ctx, 1)
			return err
		},
		"pay": func(ctx context.Context) error {
			_, err := PayBill(ctx, 1, paymentv1.Channel_CHANNEL_UNSPECIFIED, time.Time{})
			return err
		},
		"confirm": func(ctx context.Context) error {
			_, err := ConfirmBill(ctx, 1, time.Time{})
			return err
		},
		"serial number": func(ctx context.Context) error {
			_, err := SupplementSerialNumber(ctx, 1, "serial", time.Time{})
			return err
		},
		"transition": func(ctx context.Context) error {
			_, err := TransitionBill(ctx, 1, paymentv1.BillStatus_BILL_STATUS_UNPAID, time.Time{}, 20)
			return err
		},
	}
}

type billTestConnector struct{}

func (billTestConnector) Connect(context.Context) (driver.Conn, error) { return billTestConn{}, nil }
func (billTestConnector) Driver() driver.Driver                        { return billTestDriver{} }

type billTestDriver struct{}

func (billTestDriver) Open(string) (driver.Conn, error) { return billTestConn{}, nil }

type billTestConn struct{}

func (billTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (billTestConn) Close() error              { return nil }
func (billTestConn) Begin() (driver.Tx, error) { return nil, errors.New("unexpected transaction") }
func (billTestConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if !strings.HasPrefix(query, "SELECT ") {
		return nil, errors.New("unauthorized request reached a database mutation")
	}
	return &billTestRows{}, nil
}

type billTestRows struct{ consumed bool }

func (*billTestRows) Columns() []string { return []string{"id", "payer_id", "payee_id", "status"} }
func (*billTestRows) Close() error      { return nil }
func (rows *billTestRows) Next(values []driver.Value) error {
	if rows.consumed {
		return io.EOF
	}
	copy(values, []driver.Value{int64(1), int64(10), int64(20), "submitted"})
	rows.consumed = true
	return nil
}
