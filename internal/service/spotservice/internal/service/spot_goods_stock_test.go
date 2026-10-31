package service

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/user/v1/userv1connect"
	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/client"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/repository"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCreateSpotGoodsStockLimit(t *testing.T) {
	db := testutil.NewPostgres(t)
	oldDB, oldCatalog, oldUser := postgres.DB, client.CatalogInternalServiceClient, client.UserInternalServiceClient
	postgres.DB, client.CatalogInternalServiceClient, client.UserInternalServiceClient = db,
		stockLimitCatalogClient{}, stockLimitUserClient{}
	t.Cleanup(func() {
		postgres.DB, client.CatalogInternalServiceClient, client.UserInternalServiceClient = oldDB, oldCatalog, oldUser
	})
	for _, stock := range []int32{999, 1000} {
		goods := &model.SpotGoods{SellerID: 10, ProductTemplateID: 2, SalePriceCents: 100, StockTotal: stock}
		detail, err := CreateSpotGoods(context.Background(), goods, timestamppb.New(time.Unix(1, 0)))
		if stock > 999 {
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("stock 1000 returned %v, want invalid_argument", err)
			}
			continue
		}
		if err != nil || detail.Stock != 999 {
			t.Fatalf("stock 999 returned detail=%v error=%v", detail, err)
		}
	}
	count, err := db.NewSelect().Model((*model.SpotGoods)(nil)).Count(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("listing count=%d error=%v, want only stock 999 published", count, err)
	}
}

type stockLimitCatalogClient struct{ spotCreateCatalogTestClient }

func (stockLimitCatalogClient) GetProductTemplate(
	context.Context,
	*connect.Request[catalogv1.GetProductTemplateRequest],
) (*connect.Response[catalogv1.GetProductTemplateResponse], error) {
	return connect.NewResponse(&catalogv1.GetProductTemplateResponse{
		ProductTemplate: &catalogv1.ProductTemplate{Id: 2, StoreId: 1, UpdatedAt: timestamppb.New(time.Unix(1, 0))},
	}), nil
}

type stockLimitUserClient struct {
	userv1connect.UserInternalServiceClient
}

func (stockLimitUserClient) GetUsers(
	context.Context,
	*connect.Request[userv1.GetUsersRequest],
) (*connect.Response[userv1.GetUsersResponse], error) {
	return connect.NewResponse(&userv1.GetUsersResponse{Users: []*userv1.UserInfo{{Id: 10}}}), nil
}

func TestUpdateSpotGoodsStockSupportsDelistingAndRelisting(t *testing.T) {
	db := testutil.NewPostgres(t)
	previousDB := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = previousDB })
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO spot.spot_goods
		(id, seller_id, store_id, product_template_id, sale_price_cents, stock_total)
		VALUES (1, 10, 1, 1, 100, 7)`); err != nil {
		t.Fatal(err)
	}
	goods, err := repository.GetSpotGoodsByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	initialVersion := goods.UpdatedAt
	for _, change := range []struct{ stock, delta int32 }{{0, -7}, {-1, 0}, {1, 1}, {999, 998}, {0, -999}, {-1, 0}} {
		if err := UpdateSpotGoodsStock(ctx, 10, 1, change.stock, timestamppb.New(goods.UpdatedAt)); err != nil {
			t.Fatal(err)
		}
		goods, err = repository.GetSpotGoodsByID(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		assertSpotStockChange(t, goods, change.stock, change.delta)
	}
	for _, request := range []struct {
		userID  int64
		stock   int32
		version time.Time
		code    connect.Code
	}{
		{10, -2, goods.UpdatedAt, connect.CodeInvalidArgument},
		{10, 0, goods.UpdatedAt, connect.CodeInvalidArgument},
		{10, 1000, goods.UpdatedAt, connect.CodeInvalidArgument},
		{20, 5, goods.UpdatedAt, connect.CodePermissionDenied},
		{10, 5, initialVersion, connect.CodeAborted},
	} {
		if err := UpdateSpotGoodsStock(
			ctx,
			request.userID,
			1,
			request.stock,
			timestamppb.New(request.version),
		); connect.CodeOf(
			err,
		) != request.code {
			t.Fatalf("error = %v, want %v", err, request.code)
		}
	}
	count, err := db.NewSelect().Model((*model.SpotStockLedger)(nil)).Count(ctx)
	if err != nil || count != 6 {
		t.Fatalf("ledger count = %d, error = %v; want 6 successful updates", count, err)
	}
}

func TestStockPurchaseDecrementCanSellOutGoods(t *testing.T) {
	db := testutil.NewPostgres(t)
	previousDB := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = previousDB })
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO spot.spot_goods
		(id, seller_id, store_id, product_template_id, sale_price_cents, stock_total)
		VALUES (1, 10, 1, 1, 100, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := repository.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return repository.DecreaseSpotGoodsStock(ctx, tx, 1, 1)
	}); err != nil {
		t.Fatal(err)
	}
	goods, err := repository.GetSpotGoodsByID(ctx, 1)
	if err != nil || goods.StockTotal != 0 {
		t.Fatalf("natural sale should produce stock 0: goods=%v error=%v", goods, err)
	}
	if err := UpdateSpotGoodsStock(
		ctx,
		10,
		1,
		0,
		timestamppb.New(goods.UpdatedAt),
	); err != nil {
		t.Fatalf("listed sold-out goods should accept stock 0: %v", err)
	}
}

func assertSpotStockChange(t *testing.T, goods *model.SpotGoods, stock, delta int32) {
	t.Helper()
	if goods.StockTotal != stock {
		t.Fatalf("stock = %d, want %d", goods.StockTotal, stock)
	}
	var ledger model.SpotStockLedger
	if err := postgres.DB.NewSelect().
		Model(&ledger).
		OrderExpr("ssl.id DESC").
		Limit(1).
		Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ledger.Delta != delta || ledger.Reason != model.StockLedgerReasonManualAdjust {
		t.Fatalf("ledger = %+v, want manual adjustment %d", ledger, delta)
	}
}

func TestDelistingMigrationUpgradesExistingStockConstraint(t *testing.T) {
	db := testutil.NewPostgres(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `ALTER TABLE spot.spot_goods
		DROP CONSTRAINT spot_goods_stock_total_check,
		ADD CONSTRAINT spot_goods_stock_total_check CHECK(stock_total >= 0);
		INSERT INTO spot.spot_goods
		(id, seller_id, store_id, product_template_id, sale_price_cents, stock_total)
		VALUES (2, 10, 1, 1, 100, 1000)`); err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate migration")
	}
	migration, err := os.ReadFile(
		filepath.Join(filepath.Dir(file), "../../../../../migrations/003_spot_goods_delisting.sql"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO spot.spot_goods
		(id, seller_id, store_id, product_template_id, sale_price_cents, stock_total)
		VALUES (1, 10, 1, 1, 100, -1)`); err != nil {
		t.Fatalf("migrated database rejected delisting: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE spot.spot_goods SET stock_total = -2 WHERE id = 1`); err == nil {
		t.Fatal("migrated database accepted stock below -1")
	}
}
