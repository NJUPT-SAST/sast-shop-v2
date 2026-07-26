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

func TestBuildShoppingTaskSettlementPreviewAllocatesPurchasedQuantityToDemandFees(t *testing.T) {
	t.Parallel()

	actual100 := int32(100)
	actual200 := int32(200)
	purchasedThree := int32(3)
	purchasedZero := int32(0)
	purchasedTwo := int32(2)

	preview, err := buildShoppingTaskSettlementPreview(
		[]repository.ShoppingTaskItemRow{
			{TaskItemID: 1, PurchasedQuantity: &purchasedThree, ActualUnitPriceCents: &actual100},
			{TaskItemID: 2, PurchasedQuantity: &purchasedZero, ActualUnitPriceCents: &actual200},
			{TaskItemID: 3, PurchasedQuantity: nil, ActualUnitPriceCents: &actual200},
			{TaskItemID: 4, PurchasedQuantity: &purchasedTwo, ActualUnitPriceCents: nil},
		},
		[]repository.ShoppingTaskDemandFeeRow{
			{TaskItemID: 1, RequiredQuantity: 2, ServiceFeePerUnitCents: 5},
			{TaskItemID: 1, RequiredQuantity: 3, ServiceFeePerUnitCents: 7},
			{TaskItemID: 4, RequiredQuantity: 2, ServiceFeePerUnitCents: 9},
		},
	)
	if err != nil {
		t.Fatalf("buildShoppingTaskSettlementPreview() error = %v", err)
	}

	if preview.ActualProductKindCount != 2 {
		t.Fatalf("ActualProductKindCount = %d, want 2", preview.ActualProductKindCount)
	}
	if preview.ActualProductQuantity != 5 {
		t.Fatalf("ActualProductQuantity = %d, want 5", preview.ActualProductQuantity)
	}
	if preview.ProductAmountCents != 300 {
		t.Fatalf("ProductAmountCents = %d, want 300", preview.ProductAmountCents)
	}
	if preview.ServiceFeeAmountCents != 35 {
		t.Fatalf("ServiceFeeAmountCents = %d, want 35", preview.ServiceFeeAmountCents)
	}
	if preview.EstimatedTotalAmountCents != 335 {
		t.Fatalf("EstimatedTotalAmountCents = %d, want 335", preview.EstimatedTotalAmountCents)
	}
}

func TestBuildProductItemsWithZeroPackagingFee(t *testing.T) {
	t.Parallel()

	actual100 := int32(100)
	purchasedTwo := int32(2)
	items := []*model.ErrandDemandItem{
		{ID: 10, ProductTemplateID: 1000, Quantity: 2, EstimatedUnitPriceCents: 90, ServiceFeePerUnitCents: 7},
	}
	assignments := map[int64]*model.ErrandTaskAssignment{
		10: {ID: 100, DemandItemID: 10, TaskItemID: 1, DistributedQuantity: 2},
	}
	taskInfo := &buyerTaskInfo{
		TaskItemsByID: map[int64]*model.ErrandTaskItem{
			1: {ID: 1, ProductTemplateID: 1000, ActualUnitPriceCents: &actual100, PurchasedQuantity: &purchasedTwo},
		},
		TaskItemsByProduct: map[int64]*model.ErrandTaskItem{},
	}

	productItems, originCents, originServiceCents, summary, err := buildProductItems(
		items,
		assignments,
		taskInfo,
		nil,
		0,
	)
	if err != nil {
		t.Fatalf("buildProductItems() error = %v", err)
	}

	if originCents != 180 {
		t.Fatalf("originCents = %d, want 180", originCents)
	}
	if originServiceCents != 14 {
		t.Fatalf("originServiceCents = %d, want 14", originServiceCents)
	}
	if summary.ProductAmountCents != 200 ||
		summary.ServiceFeeAmountCents != 14 ||
		summary.PackagingFeeShareCents != 0 ||
		summary.TotalAmountCents != 214 {
		t.Fatalf("summary = %+v, want product=200 service=14 packaging=0 total=214", summary)
	}
	if got := productItems[0].SubtotalCents; got != 214 {
		t.Fatalf("productItems[0].SubtotalCents = %d, want 214", got)
	}
}

func TestBuildProductItemsRoundsPackagingShareAndIgnoresUndistributedItems(t *testing.T) {
	t.Parallel()

	if got := ceilDivide(101, 3); got != 34 {
		t.Fatalf("ceilDivide(101, 3) = %d, want 34", got)
	}

	actual100 := int32(100)
	actual300 := int32(300)
	purchasedFive := int32(5)
	baseTime := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	items := []*model.ErrandDemandItem{
		{ID: 10, ProductTemplateID: 1000, Quantity: 5, EstimatedUnitPriceCents: 100, ServiceFeePerUnitCents: 5},
		{ID: 11, ProductTemplateID: 1001, Quantity: 1, EstimatedUnitPriceCents: 300, ServiceFeePerUnitCents: 9},
	}
	assignments := map[int64]*model.ErrandTaskAssignment{
		10: {ID: 100, DemandItemID: 10, TaskItemID: 1, DistributedQuantity: 2},
		11: {ID: 101, DemandItemID: 11, TaskItemID: 2, DistributedQuantity: 0},
	}
	taskInfo := &buyerTaskInfo{
		TaskItemsByID: map[int64]*model.ErrandTaskItem{
			1: {
				ID:                   1,
				ProductTemplateID:    1000,
				ActualUnitPriceCents: &actual100,
				PurchasedQuantity:    &purchasedFive,
				Deadline:             baseTime,
			},
			2: {
				ID:                   2,
				ProductTemplateID:    1001,
				ActualUnitPriceCents: &actual300,
				PurchasedQuantity:    ptrInt32(0),
				Deadline:             baseTime.Add(time.Hour),
			},
		},
		TaskItemsByProduct: map[int64]*model.ErrandTaskItem{},
	}

	productItems, _, _, summary, err := buildProductItems(
		items,
		assignments,
		taskInfo,
		nil,
		ceilDivide(101, 3),
	)
	if err != nil {
		t.Fatalf("buildProductItems() error = %v", err)
	}

	if summary.ProductAmountCents != 200 ||
		summary.ServiceFeeAmountCents != 10 ||
		summary.PackagingFeeShareCents != 34 ||
		summary.TotalAmountCents != 244 {
		t.Fatalf("summary = %+v, want product=200 service=10 packaging=34 total=244", summary)
	}
	if productItems[0].PackagingFeeShareCents != 34 || productItems[0].SubtotalCents != 244 {
		t.Fatalf("distributed line = %+v, want packaging=34 subtotal=244", productItems[0])
	}
	if productItems[1].ProductAmountCents != 0 ||
		productItems[1].ServiceFeeAmountCents != 0 ||
		productItems[1].PackagingFeeShareCents != 0 ||
		productItems[1].SubtotalCents != 0 {
		t.Fatalf("undistributed line = %+v, want zero payable amounts", productItems[1])
	}
}

func ptrInt32(v int32) *int32 {
	return &v
}
