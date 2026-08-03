package service

import (
	"testing"
	"time"

	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestValidateGoodsForCreateSpotOrderAcceptsSameUTCSecond(t *testing.T) {
	t.Parallel()

	goodsUpdatedAt := time.Date(2026, 8, 3, 14, 9, 2, 0, time.FixedZone("UTC+8", 8*60*60))
	itemUpdatedAt := time.Date(2026, 8, 3, 6, 9, 2, 0, time.UTC)

	goods := &model.SpotGoods{
		UpdatedAt:  goodsUpdatedAt,
		StockTotal: 1,
	}
	item := &spotv1.CreateSpotOrder{
		Quantity:  1,
		UpdatedAt: timestamppb.New(itemUpdatedAt),
	}

	if err := validateGoodsForCreateSpotOrder(goods, item); err != nil {
		t.Fatalf("validateGoodsForCreateSpotOrder() error = %v", err)
	}
}

func TestValidateGoodsForCreateSpotOrderRejectsDifferentUTCSecond(t *testing.T) {
	t.Parallel()

	goodsUpdatedAt := time.Date(2026, 8, 3, 14, 9, 2, 0, time.FixedZone("UTC+8", 8*60*60))
	itemUpdatedAt := time.Date(2026, 8, 3, 6, 9, 3, 0, time.UTC)

	goods := &model.SpotGoods{
		UpdatedAt:  goodsUpdatedAt,
		StockTotal: 1,
	}
	item := &spotv1.CreateSpotOrder{
		Quantity:  1,
		UpdatedAt: timestamppb.New(itemUpdatedAt),
	}

	err := validateGoodsForCreateSpotOrder(goods, item)
	if code := connect.CodeOf(err); code != connect.CodeAborted {
		t.Fatalf("code = %v, want %v; err = %v", code, connect.CodeAborted, err)
	}
}

func TestValidateSpotOrderParticipantsRejectsOwnGoods(t *testing.T) {
	t.Parallel()

	err := validateSpotOrderParticipants(1, &model.SpotGoods{SellerID: 1})
	if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want %v; err = %v", code, connect.CodeFailedPrecondition, err)
	}
	if err.Error() != "cannot purchase own goods" {
		t.Fatalf("error = %v, want own-goods validation error", err)
	}
}

func TestValidateSpotOrderParticipantsAcceptsDifferentUsers(t *testing.T) {
	t.Parallel()

	if err := validateSpotOrderParticipants(2, &model.SpotGoods{SellerID: 1}); err != nil {
		t.Fatalf("validateSpotOrderParticipants() error = %v", err)
	}
}
