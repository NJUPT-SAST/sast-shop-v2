package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/repository"
)

func TestValidateSelectedDemandItemRowComparesUpdatedAtBySecond(t *testing.T) {
	t.Parallel()

	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	row := repository.SelectedDemandItemRow{
		DemandItemUpdatedAt: time.Date(2026, 7, 26, 16, 7, 52, 123456789, shanghai),
		DemandItemStatus:    model.ErrandDemandItemStatusOpen,
		DemandStatus:        model.ErrandDemandStatusOpen,
		StoreID:             10,
		Deadline:            now.Add(time.Hour),
	}
	selectedUpdatedAt := time.Date(2026, 7, 26, 8, 7, 52, 0, time.UTC)

	if err := validateSelectedDemandItemRow(row, 10, selectedUpdatedAt, now); err != nil {
		t.Fatalf("validateSelectedDemandItemRow() error = %v", err)
	}

	row.DemandItemUpdatedAt = time.Date(2026, 7, 26, 16, 7, 53, 0, shanghai)
	if err := validateSelectedDemandItemRow(row, 10, selectedUpdatedAt, now); !errors.Is(err, ErrConcurrencyConflict) {
		t.Fatalf("validateSelectedDemandItemRow() error = %v, want %v", err, ErrConcurrencyConflict)
	}
}

func TestValidateActualPriceUpdateComparesUpdatedAtBySecond(t *testing.T) {
	t.Parallel()

	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	purchasedQuantity := int32(1)
	row := &repository.DistributingTaskItemForUpdateRow{
		TaskStatus:        model.ErrandTaskStatusPendingDistributing,
		PurchasedQuantity: &purchasedQuantity,
		TaskItemUpdatedAt: time.Date(2026, 7, 26, 20, 8, 57, 276123000, shanghai),
	}
	expectedUpdatedAt := time.Date(2026, 7, 26, 12, 8, 57, 276000000, time.UTC)

	if err := validateActualPriceUpdate(row, expectedUpdatedAt, 20000); err != nil {
		t.Fatalf("validateActualPriceUpdate() error = %v", err)
	}

	row.TaskItemUpdatedAt = time.Date(2026, 7, 26, 20, 8, 58, 0, shanghai)
	if err := validateActualPriceUpdate(row, expectedUpdatedAt, 20000); !errors.Is(err, ErrConcurrencyConflict) {
		t.Fatalf("validateActualPriceUpdate() error = %v, want %v", err, ErrConcurrencyConflict)
	}
}

func TestValidateActualPriceUpdateAllowsDistributingStatus(t *testing.T) {
	t.Parallel()

	purchasedQuantity := int32(1)
	updatedAt := time.Date(2026, 7, 26, 12, 8, 57, 0, time.UTC)
	row := &repository.DistributingTaskItemForUpdateRow{
		TaskStatus:        model.ErrandTaskStatusDistributing,
		PurchasedQuantity: &purchasedQuantity,
		TaskItemUpdatedAt: updatedAt,
	}

	if err := validateActualPriceUpdate(row, updatedAt, 20000); err != nil {
		t.Fatalf("validateActualPriceUpdate() error = %v", err)
	}
}

func TestIsValidShoppingTaskItemPurchasedQuantityAllowsUndo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		purchasedQuantity int32
		requiredQuantity  int32
		want              bool
	}{
		{name: "undo", purchasedQuantity: -1, requiredQuantity: 10, want: true},
		{name: "zero", purchasedQuantity: 0, requiredQuantity: 10, want: true},
		{name: "required quantity", purchasedQuantity: 10, requiredQuantity: 10, want: true},
		{name: "less than undo sentinel", purchasedQuantity: -2, requiredQuantity: 10, want: false},
		{name: "exceeds required quantity", purchasedQuantity: 11, requiredQuantity: 10, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := isValidShoppingTaskItemPurchasedQuantity(tt.purchasedQuantity, tt.requiredQuantity)
			if got != tt.want {
				t.Fatalf("isValidShoppingTaskItemPurchasedQuantity(%d, %d) = %v, want %v",
					tt.purchasedQuantity, tt.requiredQuantity, got, tt.want)
			}
		})
	}
}

func TestCeilDivideDoesNotOverflow(t *testing.T) {
	t.Parallel()
	if got := ceilDivide(maxInt32Value, 2); got != 1073741824 {
		t.Fatalf("ceilDivide(maxInt32Value, 2) = %d, want 1073741824", got)
	}
}

func TestBuildErrandTaskStatusFilters(t *testing.T) {
	t.Parallel()

	shopping := errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_SHOPPING
	distributing := errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_DISTRIBUTING
	unspecified := errandv1.ErrandTaskStatus_ERRAND_TASK_STATUS_UNSPECIFIED

	got, err := buildErrandTaskStatusFilters(&unspecified, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("unspecified single status = %v, %v; want empty", got, err)
	}

	got, err = buildErrandTaskStatusFilters(&shopping, []errandv1.ErrandTaskStatus{
		shopping,
		distributing,
		distributing,
	})
	want := []model.ErrandTaskStatus{model.ErrandTaskStatusShopping, model.ErrandTaskStatusDistributing}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("deduplicated union = %v, %v; want %v", got, err, want)
	}

	_, err = buildErrandTaskStatusFilters(nil, []errandv1.ErrandTaskStatus{unspecified})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("unspecified in filter_statuses returned %v, want invalid_argument", err)
	}
	_, err = buildErrandTaskStatusFilters(nil, []errandv1.ErrandTaskStatus{errandv1.ErrandTaskStatus(999)})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("unknown status in filter_statuses returned %v, want invalid_argument", err)
	}
}

func TestApplyErrandTaskParticipants(t *testing.T) {
	t.Parallel()

	tasks := []*errandv1.ErrandTask{{TaskId: 10}, {TaskId: 20}}
	rows := []repository.ErrandTaskParticipantRow{
		{TaskID: 10, PurchaserID: 1, AvatarURL: "/a.png"},
		{TaskID: 10, PurchaserID: 2, AvatarURL: ""},
		{TaskID: 10, PurchaserID: 3, AvatarURL: "/c.png"},
		{TaskID: 10, PurchaserID: 4, AvatarURL: "/d.png"},
		{TaskID: 99, PurchaserID: 5, AvatarURL: "/ignored.png"},
	}
	if err := applyErrandTaskParticipants(tasks, rows); err != nil {
		t.Fatal(err)
	}
	if tasks[0].ParticipantCount != 4 || !slices.Equal(tasks[0].ParticipantAvatars, []string{"/a.png", "", "/c.png"}) {
		t.Fatalf("task 10 participants = %d %v", tasks[0].ParticipantCount, tasks[0].ParticipantAvatars)
	}
	if tasks[1].ParticipantCount != 0 || len(tasks[1].ParticipantAvatars) != 0 {
		t.Fatalf("task 20 participants = %d %v, want empty", tasks[1].ParticipantCount, tasks[1].ParticipantAvatars)
	}
}

func TestErrandOrderSearchRejectsOverlongKeyword(t *testing.T) {
	t.Parallel()

	tooLong := strings.Repeat("订", 201)
	_, err := GetErrandTaskList(context.Background(), 1, &errandv1.GetErrandTaskListRequest{
		Keyword: tooLong,
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("GetErrandTaskList overlong keyword = %v, want invalid_argument", err)
	}
	_, _, err = GetBuyerOrderBriefList(context.Background(), 1, nil, nil, tooLong, 1, 20)
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("GetBuyerOrderBriefList overlong keyword = %v, want invalid_argument", err)
	}
}
