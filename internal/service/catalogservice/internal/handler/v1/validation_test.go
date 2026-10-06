package v1

import (
	"context"
	"testing"

	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
)

func TestUpdatesRejectMissingResources(t *testing.T) {
	ctx := interceptor.SetUserToContext(
		context.Background(),
		&interceptor.AuthUser{UserID: 1, Role: "user", Status: "active"},
	)
	tests := []struct {
		name string
		call func() error
	}{
		{"store", func() error {
			_, err := (&CatalogServiceServer{}).UpdateStore(ctx, connect.NewRequest(&catalogv1.UpdateStoreRequest{}))
			return err
		}},
		{"product template", func() error {
			_, err := (&ProductTemplateServiceServer{}).UpdateProductTemplate(
				ctx,
				connect.NewRequest(&catalogv1.UpdateProductTemplateRequest{}),
			)
			return err
		}},
		{"store without ID", func() error {
			_, err := (&CatalogServiceServer{}).UpdateStore(
				ctx,
				connect.NewRequest(&catalogv1.UpdateStoreRequest{Store: &catalogv1.Store{}}),
			)
			return err
		}},
		{"product template without ID", func() error {
			_, err := (&ProductTemplateServiceServer{}).UpdateProductTemplate(
				ctx,
				connect.NewRequest(
					&catalogv1.UpdateProductTemplateRequest{ProductTemplate: &catalogv1.ProductTemplate{}},
				),
			)
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("error = %v, want invalid_argument", err)
			}
		})
	}
}
