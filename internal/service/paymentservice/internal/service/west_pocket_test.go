package service

import (
	"context"
	"errors"
	"testing"

	westpocketv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/westpocket/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/model"
)

func TestWestPocketPublicCreationAndGenericCancellationAreReserved(t *testing.T) {
	ctx := interceptor.SetUserToContext(context.Background(), &interceptor.AuthUser{UserID: 10})
	source, id := westPocketSource, int64(42)
	if _, err := CreateBill(ctx, 10, 20, 100, &source, &id); !errors.Is(err, ErrBillPermissionDenied) {
		t.Fatalf("public creation: %v", err)
	}
	if err := CancelBillBySource(ctx, source, id, nil); !errors.Is(err, ErrInvalidBillStatus) {
		t.Fatalf("generic cancellation: %v", err)
	}
}

func TestWestPocketBillsRequireFrozenStateAndMembership(t *testing.T) {
	source, id := westPocketSource, int64(42)
	bill := &model.PaymentBill{SourceType: &source, SourceID: &id, PayerID: 10, PayeeID: 20, AmountCents: 3333}
	state := &westpocketv1.GetCollectionStateResponse{
		Status:  "publishing",
		OwnerId: 20,
		Members: []*westpocketv1.PocketMember{{UserId: 10, ShareCents: 3333}},
	}
	if err := validateWestPocketBill(state, bill, "publishing"); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"draft", "collecting", "settled", "cancelling", "cancelled"} {
		state.Status = status
		if err := validateWestPocketBill(state, bill, "publishing"); !errors.Is(err, ErrInvalidBillStatus) {
			t.Fatalf("late publisher in %s: %v", status, err)
		}
	}
	state.Status = "collecting"
	if err := validateWestPocketBill(state, bill, "collecting"); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*model.PaymentBill){
		"unselected payer": func(b *model.PaymentBill) { b.PayerID = 30 },
		"other payee":      func(b *model.PaymentBill) { b.PayeeID = 30 },
		"different amount": func(b *model.PaymentBill) { b.AmountCents++ },
		"self payment":     func(b *model.PaymentBill) { b.PayerID = 20 },
		"zero amount":      func(b *model.PaymentBill) { b.AmountCents = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			copyBill := *bill
			mutate(&copyBill)
			if err := validateWestPocketBill(state, &copyBill, "collecting"); !errors.Is(err, ErrInvalidBillRequest) {
				t.Fatalf("invalid frozen payload accepted: %v", err)
			}
		})
	}
}

func TestWestPocketIdempotencyComparesCompletePayload(t *testing.T) {
	source, id := westPocketSource, int64(42)
	wanted := &model.PaymentBill{SourceType: &source, SourceID: &id, PayerID: 10, PayeeID: 20, AmountCents: 3333}
	if !sameWestPocketBill(wanted, wanted) {
		t.Fatal("equal payload did not match")
	}
	for name, mutate := range map[string]func(*model.PaymentBill){
		"source type": func(b *model.PaymentBill) { value := "spot_order"; b.SourceType = &value },
		"source id":   func(b *model.PaymentBill) { value := int64(43); b.SourceID = &value },
		"payer":       func(b *model.PaymentBill) { b.PayerID++ },
		"payee":       func(b *model.PaymentBill) { b.PayeeID++ },
		"amount":      func(b *model.PaymentBill) { b.AmountCents++ },
	} {
		t.Run(name, func(t *testing.T) {
			existing := *wanted
			mutate(&existing)
			if sameWestPocketBill(&existing, wanted) {
				t.Fatal("changed immutable field matched")
			}
		})
	}
}

func TestWestPocketStateCheckFailsClosedWithoutConfiguration(t *testing.T) {
	t.Setenv("WEST_POCKET_INTERNAL_TOKEN", "")
	source, id := westPocketSource, int64(42)
	err := requireWestPocketCollecting(context.Background(), &model.PaymentBill{SourceType: &source, SourceID: &id})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("got %v, want unavailable", err)
	}
}
