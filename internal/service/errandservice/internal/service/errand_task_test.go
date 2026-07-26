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
