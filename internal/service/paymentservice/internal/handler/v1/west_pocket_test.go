package v1

import (
	"context"
	"testing"

	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
)

func TestWestPocketInternalMethodsRequireServiceIdentity(t *testing.T) {
	t.Setenv("WEST_POCKET_INTERNAL_TOKEN", "test-internal-token")
	server := &PaymentInternalServer{}
	for _, supplied := range []string{"", "incorrect"} {
		requests := map[string]func() error{
			"qr": func() error {
				r := connect.NewRequest(&paymentv1.GetPayeeQrCodeRequest{OwnerId: 20})
				r.Header().Set(interceptor.WestPocketServiceHeader, supplied)
				_, err := server.GetPayeeQrCode(context.Background(), r)
				return err
			},
			"cancel": func() error {
				r := connect.NewRequest(&paymentv1.CancelWestPocketBillsIfUnpaidRequest{PocketId: 42})
				r.Header().Set(interceptor.WestPocketServiceHeader, supplied)
				_, err := server.CancelWestPocketBillsIfUnpaid(context.Background(), r)
				return err
			},
			"create": func() error {
				r := connect.NewRequest(
					&paymentv1.CreateBillForOrderRequest{
						SourceType:  "west_pocket",
						SourceId:    42,
						PayerId:     10,
						PayeeId:     20,
						AmountCents: 100,
					},
				)
				r.Header().Set(interceptor.WestPocketServiceHeader, supplied)
				_, err := server.CreateBillForOrder(context.Background(), r)
				return err
			},
		}
		for name, request := range requests {
			t.Run(name+"/"+supplied, func(t *testing.T) {
				if err := request(); connect.CodeOf(err) != connect.CodeUnauthenticated {
					t.Fatalf("got %v, want unauthenticated before database access", err)
				}
			})
		}
	}
}
