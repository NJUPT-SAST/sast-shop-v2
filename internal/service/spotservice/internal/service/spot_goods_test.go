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
