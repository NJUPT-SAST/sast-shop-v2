package interceptor

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"os"

	"connectrpc.com/connect"
)

// WestPocketServiceHeader is reserved for trusted server-to-server calls.
// Public proxies must strip it before forwarding browser requests.
const WestPocketServiceHeader = "X-West-Pocket-Token"

// RequireWestPocketService deliberately has no development bypass. An unset
// token disables internal access instead of accepting an empty credential.
func RequireWestPocketService(header http.Header) error {
	expected := os.Getenv("WEST_POCKET_INTERNAL_TOKEN")
	provided := header.Get(WestPocketServiceHeader)
	if len(expected) < 32 || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("internal service authentication required"))
	}
	return nil
}
