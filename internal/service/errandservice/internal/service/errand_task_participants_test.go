package service

import (
	"context"
	"slices"
	"testing"

	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
)

func TestGetErrandTaskParticipants(t *testing.T) {
	db := testutil.NewPostgres(t)
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = previous })
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		INSERT INTO "user".user_account (id, feishu_open_id, display_name, avatar_url)
		VALUES (1, 'user-1', 'A', '/a.png'), (3, 'user-3', 'C', '/c.png'), (4, 'user-4', 'D', '/d.png');
		INSERT INTO errand.errand_task (id, task_no, captain_id, store_id)
		VALUES (10, 'task-10', 1, 1), (20, 'task-20', 2, 1), (30, 'task-30', 1, 1);
		INSERT INTO errand.errand_task_item (id, task_id, product_template_id, title_snapshot, required_quantity, deadline)
		SELECT 100 + id, CASE WHEN id = 7 THEN 20 ELSE 10 END, id, 'Item', 1, now()
		FROM generate_series(1,7) AS id;
		INSERT INTO errand.errand_demand (id, requester_id, store_id, deadline)
		SELECT id, CASE WHEN id <= 2 THEN 1 WHEN id = 6 THEN 99 WHEN id = 7 THEN 999 ELSE id - 1 END,
		1, now() + interval '1 day' FROM generate_series(1,7) AS id;
		INSERT INTO errand.errand_demand_item
		(id, errand_demand_id, requester_id, store_id, product_template_id, estimated_unit_price_cents, quantity, service_fee_per_unit_cents)
		SELECT id, id, CASE WHEN id <= 2 THEN 1 WHEN id = 6 THEN 99 WHEN id = 7 THEN 999 ELSE id - 1 END,
		1, id, 100, 1, 10 FROM generate_series(1,7) AS id;
		INSERT INTO errand.errand_task_assignment (task_id, task_item_id, demand_item_id, purchaser_id, service_fee_per_unit_cents)
		VALUES (10, 101, 1, 1, 10), (10, 102, 2, 1, 10), (10, 103, 3, 2, 10),
		(10, 104, 4, 3, 10), (10, 105, 5, 4, 10), (10, 106, 6, 99, 10), (20, 107, 7, 999, 10);
	`)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"shopping", "pending_distributing", "distributing", "collecting_payment", "completed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			_, updateErr := db.ExecContext(ctx, "UPDATE errand.errand_task SET status = ? WHERE id = 10", status)
			if updateErr != nil {
				t.Fatal(updateErr)
			}
			response, loadErr := GetErrandTaskParticipants(
				ctx,
				1,
				&errandv1.GetErrandTaskParticipantsRequest{ErrandTaskId: 10},
			)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if response.ParticipantCount != 5 ||
				!slices.Equal(response.ParticipantAvatars, []string{"/a.png", "", "/c.png"}) {
				t.Fatalf("wrong distinct count or avatar preview: %v", response)
			}
		})
	}
	if _, err = GetErrandTaskParticipants(
		ctx,
		2,
		&errandv1.GetErrandTaskParticipantsRequest{ErrandTaskId: 10},
	); connect.CodeOf(
		err,
	) != connect.CodeNotFound {
		t.Fatalf("another captain can read participants: %v", err)
	}
	response, err := GetErrandTaskParticipants(ctx, 1, &errandv1.GetErrandTaskParticipantsRequest{ErrandTaskId: 30})
	if err != nil || response.ParticipantCount != 0 || len(response.ParticipantAvatars) != 0 {
		t.Fatalf("empty task: response %v, error %v", response, err)
	}
	if _, err = GetErrandTaskParticipants(ctx, 1, nil); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("nil request: %v", err)
	}
}
