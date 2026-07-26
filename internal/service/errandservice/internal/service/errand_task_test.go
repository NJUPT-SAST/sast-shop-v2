package service

import (
	"errors"
	"testing"
	"time"

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
