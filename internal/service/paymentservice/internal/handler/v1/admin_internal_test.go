package v1

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/adminflow"
)

func TestInternalAdminAuthenticationRejectsMissingAndForgedTokens(t *testing.T) {
	for _, tc := range []struct{ name, configured, supplied string }{
		{"unconfigured", "", ""},
		{"too short", "short", "short"},
		{"missing", strings.Repeat("a", 40), ""},
		{"forged", strings.Repeat("a", 40), strings.Repeat("b", 40)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ADMIN_INTERNAL_TOKEN", tc.configured)
			header := http.Header{}
			header.Set(adminflow.InternalHeader, tc.supplied)
			if _, err := internalAdminContext(
				context.Background(),
				header,
				"42",
			); connect.CodeOf(
				err,
			) != connect.CodePermissionDenied {
				t.Fatalf("internal endpoint accepted invalid credentials: %v", err)
			}
		})
	}
}
