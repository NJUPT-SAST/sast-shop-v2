package v1

import (
	"context"
	"fmt"
	"testing"

	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/service"
)

func TestMapAddressNotFound(t *testing.T) {
	err := mapAddressError(fmt.Errorf("lookup failed: %w", service.ErrAddressNotFound))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("error = %v, want not_found", err)
	}
}

func TestAddressRequiresAuthentication(t *testing.T) {
	server := &AddressServer{}
	_, err := server.GetAddress(context.Background(), connect.NewRequest(&userv1.GetAddressRequest{}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("error = %v, want unauthenticated", err)
	}
}
