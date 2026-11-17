package admin

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
)

func TestAdminCapabilitiesRequireActiveTrustedRole(t *testing.T) {
	for _, test := range []struct {
		role, status     string
		adminOK, superOK bool
	}{
		{"user", "active", false, false},
		{"admin", "active", true, false},
		{"superadmin", "active", true, true},
		{"superadmin", "banned", false, false},
		{"ADMIN", "active", false, false},
		{"admin", "", false, false},
	} {
		ctx := interceptor.SetUserToContext(
			context.Background(),
			&interceptor.AuthUser{UserID: 1, Role: test.role, Status: test.status},
		)
		_, err := Require(ctx)
		if (err == nil) != test.adminOK {
			t.Errorf("Require(%+v): %v", test, err)
		}
		_, err = RequireSuper(ctx)
		if (err == nil) != test.superOK {
			t.Errorf("RequireSuper(%+v): %v", test, err)
		}
	}
	if _, err := Require(context.Background()); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal(err)
	}
}
