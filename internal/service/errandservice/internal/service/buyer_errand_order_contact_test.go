package service

import (
	"context"
	"errors"
	"testing"

	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/repository"
)

func TestGetBuyerErrandOrderCaptainContact(t *testing.T) {
	t.Parallel()

	const (
		requesterID = int64(101)
		captainID   = int64(202)
		demandID    = int64(303)
		openID      = "ou_captain"
	)

	tests := []struct {
		name       string
		userID     int64
		req        *errandv1.GetBuyerErrandOrderCaptainContactRequest
		record     *repository.BuyerErrandOrderContactRecord
		recordErr  error
		resolvedID string
		wantOpenID string
		wantCode   connect.Code
	}{
		{
			name:     "rejects invalid request",
			userID:   requesterID,
			req:      &errandv1.GetBuyerErrandOrderCaptainContactRequest{},
			wantCode: connect.CodeInvalidArgument,
		},
		{
			name:      "returns not found for unknown order",
			userID:    requesterID,
			req:       &errandv1.GetBuyerErrandOrderCaptainContactRequest{ErrandDemandId: demandID},
			recordErr: repository.ErrNotFound,
			wantCode:  connect.CodeNotFound,
		},
		{
			name:   "does not disclose captain contact to non requester",
			userID: captainID,
			req:    &errandv1.GetBuyerErrandOrderCaptainContactRequest{ErrandDemandId: demandID},
			record: &repository.BuyerErrandOrderContactRecord{
				RequesterID: requesterID,
				CaptainID:   int64Pointer(captainID),
			},
			wantCode: connect.CodePermissionDenied,
		},
		{
			name:     "reports contact unavailable before captain assignment",
			userID:   requesterID,
			req:      &errandv1.GetBuyerErrandOrderCaptainContactRequest{ErrandDemandId: demandID},
			record:   &repository.BuyerErrandOrderContactRecord{RequesterID: requesterID},
			wantCode: connect.CodeFailedPrecondition,
		},
		{
			name:   "returns captain contact to requester",
			userID: requesterID,
			req:    &errandv1.GetBuyerErrandOrderCaptainContactRequest{ErrandDemandId: demandID},
			record: &repository.BuyerErrandOrderContactRecord{
				RequesterID: requesterID,
				CaptainID:   int64Pointer(captainID),
			},
			resolvedID: openID,
			wantOpenID: openID,
		},
		{
			name:   "rejects blank captain contact",
			userID: requesterID,
			req:    &errandv1.GetBuyerErrandOrderCaptainContactRequest{ErrandDemandId: demandID},
			record: &repository.BuyerErrandOrderContactRecord{
				RequesterID: requesterID,
				CaptainID:   int64Pointer(captainID),
			},
			resolvedID: "  ",
			wantCode:   connect.CodeFailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resolverCalled := false
			deps := buyerErrandOrderContactDependencies{
				getOrderContactRecord: func(context.Context, int64) (*repository.BuyerErrandOrderContactRecord, error) {
					return tt.record, tt.recordErr
				},
				resolveContactOpenID: func(_ context.Context, gotUserID int64) (string, error) {
					resolverCalled = true
					if gotUserID != captainID {
						t.Fatalf("resolveContactOpenID userID = %d, want %d", gotUserID, captainID)
					}
					return tt.resolvedID, nil
				},
			}

			got, err := getBuyerErrandOrderCaptainContact(context.Background(), tt.userID, tt.req, deps)
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
				t.Fatalf("getBuyerErrandOrderCaptainContact() error = %v", err)
			}
			if got != tt.wantOpenID {
				t.Fatalf("openID = %q, want %q", got, tt.wantOpenID)
			}
		})
	}
}

func TestGetBuyerErrandOrderCaptainContactRepositoryFailure(t *testing.T) {
	t.Parallel()

	deps := buyerErrandOrderContactDependencies{
		getOrderContactRecord: func(context.Context, int64) (*repository.BuyerErrandOrderContactRecord, error) {
			return nil, errors.New("database unavailable")
		},
		resolveContactOpenID: func(context.Context, int64) (string, error) {
			t.Fatal("contact resolver called after repository failure")
			return "", nil
		},
	}

	_, err := getBuyerErrandOrderCaptainContact(
		context.Background(),
		101,
		&errandv1.GetBuyerErrandOrderCaptainContactRequest{ErrandDemandId: 303},
		deps,
	)
	if code := connect.CodeOf(err); code != connect.CodeInternal {
		t.Fatalf("code = %v, want %v; err = %v", code, connect.CodeInternal, err)
	}
}

func int64Pointer(value int64) *int64 {
	return &value
}
