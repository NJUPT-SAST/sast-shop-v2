package service

import (
	"context"
	"errors"
	"testing"

	spotv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/spot/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/repository"
)

func TestGetSpotOrderSellerContact(t *testing.T) {
	t.Parallel()

	const (
		purchaserID = int64(101)
		sellerID    = int64(202)
		orderID     = int64(303)
		openID      = "ou_seller"
	)

	tests := []struct {
		name       string
		userID     int64
		req        *spotv1.GetSpotOrderSellerContactRequest
		order      *repository.SpotOrderRecord
		orderErr   error
		resolvedID string
		wantOpenID string
		wantCode   connect.Code
	}{
		{
			name:     "rejects invalid request",
			userID:   purchaserID,
			req:      &spotv1.GetSpotOrderSellerContactRequest{},
			wantCode: connect.CodeInvalidArgument,
		},
		{
			name:     "returns not found for unknown order",
			userID:   purchaserID,
			req:      &spotv1.GetSpotOrderSellerContactRequest{SpotOrderId: orderID},
			orderErr: repository.ErrNotFound,
			wantCode: connect.CodeNotFound,
		},
		{
			name:     "does not disclose seller contact to non purchaser",
			userID:   sellerID,
			req:      &spotv1.GetSpotOrderSellerContactRequest{SpotOrderId: orderID},
			order:    &repository.SpotOrderRecord{PurchaserID: purchaserID, SellerID: sellerID},
			wantCode: connect.CodePermissionDenied,
		},
		{
			name:       "returns seller contact to purchaser",
			userID:     purchaserID,
			req:        &spotv1.GetSpotOrderSellerContactRequest{SpotOrderId: orderID},
			order:      &repository.SpotOrderRecord{PurchaserID: purchaserID, SellerID: sellerID},
			resolvedID: openID,
			wantOpenID: openID,
		},
		{
			name:       "rejects blank seller contact",
			userID:     purchaserID,
			req:        &spotv1.GetSpotOrderSellerContactRequest{SpotOrderId: orderID},
			order:      &repository.SpotOrderRecord{PurchaserID: purchaserID, SellerID: sellerID},
			resolvedID: "  ",
			wantCode:   connect.CodeFailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resolverCalled := false
			deps := spotOrderContactDependencies{
				getOrderRecord: func(context.Context, int64) (*repository.SpotOrderRecord, error) {
					return tt.order, tt.orderErr
				},
				resolveContactOpenID: func(_ context.Context, gotUserID int64) (string, error) {
					resolverCalled = true
					if gotUserID != sellerID {
						t.Fatalf("resolveContactOpenID userID = %d, want %d", gotUserID, sellerID)
					}
					return tt.resolvedID, nil
				},
			}

			got, err := getSpotOrderSellerContact(context.Background(), tt.userID, tt.req, deps)
			if tt.wantCode != 0 {
				if code := connect.CodeOf(err); code != tt.wantCode {
					t.Fatalf("code = %v, want %v; err = %v", code, tt.wantCode, err)
				}
				if tt.wantCode == connect.CodePermissionDenied && resolverCalled {
					t.Fatal("contact resolver called before authorization")
				}
				return
			}
			if err != nil {
				t.Fatalf("getSpotOrderSellerContact() error = %v", err)
			}
			if got != tt.wantOpenID {
				t.Fatalf("openID = %q, want %q", got, tt.wantOpenID)
			}
		})
	}
}

func TestGetSpotOrderSellerContactRepositoryFailure(t *testing.T) {
	t.Parallel()

	deps := spotOrderContactDependencies{
		getOrderRecord: func(context.Context, int64) (*repository.SpotOrderRecord, error) {
			return nil, errors.New("database unavailable")
		},
		resolveContactOpenID: func(context.Context, int64) (string, error) {
			t.Fatal("contact resolver called after repository failure")
			return "", nil
		},
	}

	_, err := getSpotOrderSellerContact(
		context.Background(),
		101,
		&spotv1.GetSpotOrderSellerContactRequest{SpotOrderId: 303},
		deps,
	)
	if code := connect.CodeOf(err); code != connect.CodeInternal {
		t.Fatalf("code = %v, want %v; err = %v", code, connect.CodeInternal, err)
	}
}
