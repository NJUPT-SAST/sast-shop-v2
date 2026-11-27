package service

import (
	"testing"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
)

func TestModelToBriefUsesListingStock(t *testing.T) {
	t.Parallel()

	for _, stock := range []int32{-1, 0, 1, 25} {
		goods := &model.SpotGoods{ID: 1, StoreID: 2, StockTotal: stock}
		brief := modelToBrief(goods, &catalogv1.ProductTemplate{Id: 3, StoreId: 2})
		if brief.Stock != stock {
			t.Fatalf("listing stock = %d, want %d", brief.Stock, stock)
		}
		if brief.Id != goods.ID || brief.ProductTemplate.Id != 3 {
			t.Fatalf("unexpected listing or template mapping: %v", brief)
		}
	}
}

func TestListingTemplateCategoryFollowsItsStore(t *testing.T) {
	t.Parallel()

	categoryID := int64(21)
	template := &catalogv1.ProductTemplate{Id: 3, StoreId: 7, CategoryId: &categoryID}
	originalListing := &model.SpotGoods{StoreID: 2}
	for _, embedded := range []*catalogv1.ProductTemplate{
		modelToBrief(originalListing, template).GetProductTemplate(),
		modelToDetail(originalListing, template, nil).GetProductTemplate(),
	} {
		if embedded.GetId() != 3 || embedded.GetStoreId() != 2 || embedded.CategoryId != nil {
			t.Errorf("embedded template = %v, want template 3 at store 2 without a category", embedded)
		}
	}

	currentListing := &model.SpotGoods{StoreID: 7}
	embedded := modelToBrief(currentListing, template).GetProductTemplate()
	if embedded.GetStoreId() != 7 || embedded.GetCategoryId() != 21 {
		t.Errorf("embedded template = %v, want store 7 with category 21", embedded)
	}
}
