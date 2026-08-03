package v1

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/service"
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
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := connect.CodeOf(mapServiceError(testCase.err)); got != testCase.code {
				t.Fatalf("mapServiceError(%v) code = %v, want %v", testCase.err, got, testCase.code)
			}
		})
	}
}
