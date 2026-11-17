package service

import (
	"context"
	"testing"
	"time"

	cc "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/catalog/v1/catalogv1connect"
	cv "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	ev "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/client"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type governedErrandCatalog struct {
	cc.CatalogInternalServiceClient
	version time.Time
}

func (c governedErrandCatalog) GetProductTemplates(
	context.Context,
	*connect.Request[cv.GetProductTemplatesRequest],
) (*connect.Response[cv.GetProductTemplatesResponse], error) {
	return connect.NewResponse(
		&cv.GetProductTemplatesResponse{
			ProductTemplates: []*cv.ProductTemplate{
				{Id: 60, StoreId: 50, Title: "商品", PriceCents: 100, UpdatedAt: timestamppb.New(c.version)},
			},
		},
	), nil
}

func TestErrandGovernanceBlocksNewTradesAndPreservesFulfilment(t *testing.T) {
	for _, blocked := range []string{"store", "template"} {
		t.Run(blocked, func(t *testing.T) {
			ctx, db := setupErrandLifecycle(t)
			version := time.Now().UTC()
			oldClient := client.CatalogInternalClient
			client.CatalogInternalClient = governedErrandCatalog{version: version}
			t.Cleanup(func() { client.CatalogInternalClient = oldClient })
			seedErrandGovernance(t, ctx, db, blocked)
			var demand model.ErrandDemand
			var item model.ErrandDemandItem
			if err := db.NewSelect().Model(&demand).Where("id=200").Scan(ctx); err != nil {
				t.Fatal(err)
			}
			if err := db.NewSelect().Model(&item).Where("id=300").Scan(ctx); err != nil {
				t.Fatal(err)
			}
			drafts := []DemandItemDraft{
				{ProductTemplateID: 60, Quantity: 2, ServiceFeePerUnitCents: 10, UpdatedAt: version},
			}
			for name, call := range map[string]func() error{
				"create demand": func() error { _, err := CreateErrandDemand(ctx, 20, 50, demand.Deadline, drafts); return err },
				"edit demand": func() error {
					_, err := UpdateErrandDemand(ctx, 20, &UpdateErrandDemandParams{DemandID: 200, StoreID: 50, Deadline: demand.Deadline, Items: drafts, ExpectedUpdatedAt: demand.UpdatedAt, LifecycleRound: 2})
					return err
				},
				"accept demand": func() error {
					_, err := CreateTask(ctx, 10, &ev.CreateTaskRequest{StoreId: 50, DemandItems: []*ev.TaskDemandItem{{ErrandDemandItemId: 300, UpdatedAt: timestamppb.New(item.UpdatedAt), DemandLifecycleRound: 2}}})
					return err
				},
			} {
				t.Run(name, func(t *testing.T) {
					if err := call(); connect.CodeOf(err) != connect.CodeFailedPrecondition {
						t.Fatalf("catalog governance bypass: %v", err)
					}
				})
			}
			assertGovernedErrandFulfilment(t, ctx, db)
		})
	}
}

func seedErrandGovernance(t *testing.T, ctx context.Context, db *bun.DB, blocked string) {
	t.Helper()
	_, err := db.ExecContext(
		ctx,
		`INSERT INTO catalog.catalog_store(id,name,address,created_by_user_id) VALUES(50,'Store','',10);
INSERT INTO catalog.catalog_product_template(id,title,price_cents,store_id,created_by_user_id) VALUES(60,'商品',100,50,10);
DELETE FROM errand.errand_task_assignment;
UPDATE errand.errand_demand SET status='open',task_id=NULL WHERE id=200;
UPDATE errand.errand_demand_item SET status='open' WHERE id=300;
UPDATE errand.errand_task SET status='shopping' WHERE id=100`,
	)
	if err != nil {
		t.Fatal(err)
	}
	table := "catalog.catalog_store"
	if blocked == "template" {
		table = "catalog.catalog_product_template"
	}
	if _, err := db.NewUpdate().
		TableExpr("?", bun.Ident(table)).
		Set("moderation_status='blocked'").
		Where("id IN (50,60)").
		Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

func assertGovernedErrandFulfilment(t *testing.T, ctx context.Context, db *bun.DB) {
	t.Helper()
	var item model.ErrandTaskItem
	if err := db.NewSelect().Model(&item).Where("id=400").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := SaveShoppingTaskItem(
		ctx,
		10,
		&ev.SaveShoppingTaskItemRequest{
			ErrandTaskId:            100,
			ErrandTaskItemId:        400,
			PurchasedQuantity:       1,
			ErrandTaskItemUpdatedAt: timestamppb.New(item.UpdatedAt),
			LifecycleRound:          2,
		},
	)
	if err != nil {
		t.Fatalf("existing fulfilment blocked: %v", err)
	}
	count, err := db.NewSelect().Model((*model.ErrandDemand)(nil)).Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("new demand escaped governance: count=%d err=%v", count, err)
	}
}
