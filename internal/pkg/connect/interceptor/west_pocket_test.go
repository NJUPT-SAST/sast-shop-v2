package interceptor

import (
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
)

func TestWestPocketServiceCredentialFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name, configured, provided string
		allowed                    bool
	}{
		{name: "missing configuration"},
		{name: "short credential", configured: "secret", provided: "secret"},
		{name: "browser without credential", configured: strings.Repeat("a", 32)},
		{name: "wrong credential", configured: strings.Repeat("a", 32), provided: strings.Repeat("b", 32)},
		{name: "trusted caller", configured: strings.Repeat("a", 32), provided: strings.Repeat("a", 32), allowed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("WEST_POCKET_INTERNAL_TOKEN", test.configured)
			header := make(http.Header)
			header.Set(WestPocketServiceHeader, test.provided)
			err := RequireWestPocketService(header)
			if test.allowed && err != nil {
				t.Fatal(err)
			}
			if !test.allowed && connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatalf("got %v, want unauthenticated", err)
			}
		})
	}
}
