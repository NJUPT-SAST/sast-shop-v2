package v1

import (
	"context"
	"testing"

	wp "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/westpocket/v1"
	"connectrpc.com/connect"
)

func TestInternalCollectionRejectsMissingIdentity(t *testing.T) {
	t.Setenv("WEST_POCKET_INTERNAL_TOKEN", "secret")
	h := Handler{}
	_, e := h.GetCollectionState(context.Background(), connect.NewRequest(&wp.GetCollectionStateRequest{PocketId: 1}))
	if connect.CodeOf(e) != connect.CodeUnauthenticated && connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatalf("missing identity: %v", e)
	}
}
