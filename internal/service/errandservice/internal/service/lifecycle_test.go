package service

import (
	"context"
	"testing"

	ev "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func setupErrandLifecycle(t *testing.T) (context.Context, *bun.DB) {
	t.Helper()
	db := testutil.NewPostgres(
		t,
		"002_west_pocket.sql",
		"005_admin_identity.sql",
		"006_admin_catalog_spot.sql",
		"007_admin_corrections.sql",
		"008_admin_errand_pocket.sql",
	)
	oldDB := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = oldDB })
	ctx := context.Background()
	_, err := db.ExecContext(
		ctx,
		`INSERT INTO errand.errand_task(id,task_no,captain_id,store_id,status,lifecycle_round) VALUES (100,'TASK100',10,50,'distributing',2);
INSERT INTO errand.errand_demand(id,requester_id,store_id,status,deadline,task_id,lifecycle_round) VALUES (200,20,50,'distributing',now()+interval '1 day',100,2);
INSERT INTO errand.errand_demand_item(id,errand_demand_id,requester_id,store_id,product_template_id,estimated_unit_price_cents,quantity,service_fee_per_unit_cents,status) VALUES (300,200,20,50,60,100,2,10,'distributing');
INSERT INTO errand.errand_task_item(id,task_id,product_template_id,title_snapshot,required_quantity,purchased_quantity,actual_unit_price_cents,deadline) VALUES (400,100,60,'商品',2,2,100,now()+interval '1 day');
INSERT INTO errand.errand_task_assignment(id,task_id,task_item_id,demand_item_id,purchaser_id,distributed_quantity,service_fee_per_unit_cents) VALUES (500,100,400,300,20,2,10)`,
	)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, db
}

func TestConfirmShoppingPrefillsPurchasedItemPrices(t *testing.T) {
	ctx, db := setupErrandLifecycle(t)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO catalog.catalog_store (id, name, address, created_by_user_id)
		VALUES (50, '奥乐齐', '', 10);
		INSERT INTO catalog.catalog_product_template (id, title, price_cents, store_id, created_by_user_id)
		VALUES (60, '商品', 125, 50, 10), (61, '已有价格', 200, 50, 10),
		       (62, '未采购', 220, 50, 10), (63, '零元商品', 0, 50, 10);
		UPDATE errand.errand_task SET status = 'shopping' WHERE id = 100;
		UPDATE errand.errand_task_item SET actual_unit_price_cents = NULL, purchased_quantity = 1 WHERE id = 400;
		INSERT INTO errand.errand_task_item
			(id, task_id, product_template_id, title_snapshot, required_quantity, purchased_quantity, actual_unit_price_cents, deadline)
		VALUES (401, 100, 61, '已有价格', 2, 2, 180, now()),
		       (402, 100, 62, '未采购', 2, 0, NULL, now()),
		       (403, 100, 63, '零元商品', 2, 2, NULL, now()),
		       (404, 100, 64, '缺少模板', 2, 2, NULL, now());
	`); err != nil {
		t.Fatal(err)
	}
	var task model.ErrandTask
	if err := db.NewSelect().Model(&task).Where("id = 100").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	updatedAt, err := TransitionToPendingDistributing(ctx, 10, &ev.TransitionToPendingDistributingRequest{
		ErrandTaskId:   task.ID,
		UpdatedAt:      timestamppb.New(task.UpdatedAt),
		LifecycleRound: task.LifecycleRound,
	})
	if err != nil {
		t.Fatal(err)
	}

	var items []model.ErrandTaskItem
	if err := db.NewSelect().Model(&items).Where("task_id = 100").Order("id").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("got %d task items, want 5", len(items))
	}
	for i, want := range []int32{125, 180, -1, 0, -1} {
		price := items[i].ActualUnitPriceCents
		if want == -1 {
			if price != nil {
				t.Errorf("item %d acquired a price despite no purchase or missing template", items[i].ID)
			}
		} else if price == nil || *price != want {
			t.Errorf("item %d price = %v, want %d cents", items[i].ID, price, want)
		}
	}
	if !items[0].UpdatedAt.Equal(updatedAt.AsTime()) {
		t.Fatalf("prefilled price did not advance item version to the transition timestamp")
	}
	if _, err := UpdateActualPrice(ctx, 10, &ev.UpdateActualPriceRequest{
		ErrandTaskId:            task.ID,
		ErrandTaskItemId:        items[0].ID,
		ErrandTaskItemUpdatedAt: timestamppb.New(items[0].UpdatedAt),
		ActualUnitPriceCents:    135,
		LifecycleRound:          task.LifecycleRound,
	}); err != nil {
		t.Fatalf("editing prefilled price: %v", err)
	}
	var newPrice int32
	if err := db.NewSelect().Model((*model.ErrandTaskItem)(nil)).
		Column("actual_unit_price_cents").Where("id = 400").Scan(ctx, &newPrice); err != nil {
		t.Fatal(err)
	}
	if newPrice != 135 {
		t.Errorf("price after manual edit = %d cents, want 135", newPrice)
	}
}

func TestErrandOldRoundCannotWriteItemsAssignmentsOrCreateBills(t *testing.T) {
	ctx, db := setupErrandLifecycle(t)
	var task model.ErrandTask
	if err := db.NewSelect().Model(&task).Where("id=100").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"shopping item": func() error {
			_, err := SaveShoppingTaskItem(ctx, 10, &ev.SaveShoppingTaskItemRequest{ErrandTaskId: 100, ErrandTaskItemId: 400, PurchasedQuantity: 1, ErrandTaskItemUpdatedAt: timestamppb.Now(), LifecycleRound: 1})
			return err
		},
		"assignment": func() error {
			_, err := SaveDistributingTaskAssignment(ctx, 10, &ev.SaveDistributingTaskAssignmentRequest{ErrandTaskItemId: 400, ErrandTaskAssignmentId: 500, DistributedQuantity: 1, ErrandTaskAssignmentUpdatedAt: timestamppb.Now(), LifecycleRound: 1})
			return err
		},
		"collect payment": func() error {
			_, err := TransitionToCollectingPayment(ctx, 10, &ev.TransitionToCollectingPaymentRequest{ErrandTaskId: 100, UpdatedAt: timestamppb.New(task.UpdatedAt), LifecycleRound: 1})
			return err
		},
		"cancel": func() error {
			_, err := CancelTask(ctx, 10, &ev.CancelTaskRequest{ErrandTaskId: 100, UpdatedAt: timestamppb.New(task.UpdatedAt), LifecycleRound: 1})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatalf("old request accepted: %v", err)
			}
		})
	}
}

func TestErrandOldPaymentCallbacksAndParentMarkerAreRejected(t *testing.T) {
	ctx, db := setupErrandLifecycle(t)
	if _, err := db.ExecContext(
		ctx,
		`UPDATE errand.errand_task SET status='collecting_payment' WHERE id=100`,
	); err != nil {
		t.Fatal(err)
	}
	for _, call := range []func() error{
		func() error { return OnErrandTaskPaymentConfirmed(ctx, 100, 20, 1) },
		func() error { return OnErrandTaskAllPaymentsConfirmed(ctx, 100, 1) },
	} {
		if err := call(); connect.CodeOf(err) != connect.CodeAborted {
			t.Fatalf("old callback accepted: %v", err)
		}
	}
	if _, err := db.ExecContext(
		ctx,
		`UPDATE errand.errand_task SET admin_operation_id='reserved-operation' WHERE id=100`,
	); err != nil {
		t.Fatal(err)
	}
	err := OnErrandTaskPaymentConfirmed(ctx, 100, 20, 2)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("callback ignored parent lock: %v", err)
	}
	var status string
	if err := db.NewRaw(`SELECT status FROM errand.errand_demand WHERE id=200`).Scan(ctx, &status); err != nil {
		t.Fatal(err)
	}
	if status != "distributing" {
		t.Fatalf("stale callback changed demand: %s", status)
	}
}

func TestErrandDemandRoundGuardsSelectionAndCancellation(t *testing.T) {
	ctx, db := setupErrandLifecycle(t)
	if _, err := db.ExecContext(
		ctx,
		`DELETE FROM errand.errand_task_assignment;UPDATE errand.errand_demand SET status='open',task_id=NULL WHERE id=200;UPDATE errand.errand_demand_item SET status='open' WHERE id=300`,
	); err != nil {
		t.Fatal(err)
	}
	var demand model.ErrandDemand
	var item model.ErrandDemandItem
	if err := db.NewSelect().Model(&demand).Where("id=200").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.NewSelect().Model(&item).Where("id=300").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := CancelErrandDemand(ctx, 20, 200, demand.UpdatedAt, 1)
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("old cancellation accepted: %v", err)
	}
	_, err = CreateTask(
		ctx,
		10,
		&ev.CreateTaskRequest{
			StoreId: 50,
			DemandItems: []*ev.TaskDemandItem{
				{ErrandDemandItemId: 300, UpdatedAt: timestamppb.New(item.UpdatedAt), DemandLifecycleRound: 1},
			},
		},
	)
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("old selection accepted: %v", err)
	}
	if _, err = CancelErrandDemand(ctx, 20, 200, demand.UpdatedAt, 2); err != nil {
		t.Fatalf("current round rejected: %v", err)
	}
}

func TestErrandDoesNotBindBillsAfterAllocationChanges(t *testing.T) {
	ctx, db := setupErrandLifecycle(t)
	bills := []boundBillDraft{{PayerID: 20, PayeeID: 10, BillID: 600, AmountCents: 220}}
	if _, err := db.ExecContext(
		ctx,
		`UPDATE errand.errand_task_assignment SET distributed_quantity=1 WHERE id=500`,
	); err != nil {
		t.Fatal(err)
	}
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := guardErrandTask(ctx, tx, 100, 10, 2); err != nil {
			return err
		}
		return validateErrandBoundBills(ctx, tx, 100, bills)
	})
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("changed allocation accepted old bill: %v", err)
	}
}
