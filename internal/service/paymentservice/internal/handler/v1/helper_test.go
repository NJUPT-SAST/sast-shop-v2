package v1

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestMapServiceErrorUsesClientFacingCodes(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		err  error
		code connect.Code
	}{
		{"bill not found", service.ErrBillNotFound, connect.CodeNotFound},
		{"invalid status", service.ErrInvalidBillStatus, connect.CodeFailedPrecondition},
		{"invalid channel", service.ErrInvalidChannel, connect.CodeInvalidArgument},
		{"duplicate bill", service.ErrDuplicateBill, connect.CodeAlreadyExists},
		{"concurrency conflict", service.ErrConcurrencyConflict, connect.CodeAborted},
		{"permission denied", service.ErrBillPermissionDenied, connect.CodePermissionDenied},
		{"invalid request", service.ErrInvalidBillRequest, connect.CodeInvalidArgument},
		{"self payment", service.ErrSelfPayment, connect.CodeInvalidArgument},
		{"unauthenticated", connect.NewError(connect.CodeUnauthenticated, nil), connect.CodeUnauthenticated},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := connect.CodeOf(mapServiceError(testCase.err)); got != testCase.code {
				t.Fatalf("mapServiceError(%v) code = %v, want %v", testCase.err, got, testCase.code)
			}
		})
	}
}

func TestRequireUpdatedAt(t *testing.T) {
	t.Parallel()
	for _, ts := range []*timestamppb.Timestamp{nil, {Seconds: 253402300800}, {Nanos: -1}} {
		if _, err := requireUpdatedAt(ts); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("requireUpdatedAt(%v) error = %v, want invalid argument", ts, err)
		}
	}
	now := time.Now().UTC()
	if got, err := requireUpdatedAt(timestamppb.New(now)); err != nil || !got.Equal(now) {
		t.Fatalf("requireUpdatedAt(valid) = %v, %v; want %v, nil", got, err, now)
	}
}
