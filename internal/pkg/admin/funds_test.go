package admin

import (
	"testing"

	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
)

func TestSourceFundsPreflightMatchesPaymentHistory(t *testing.T) {
	cases := []struct {
		name, action, mode string
		bill               *av.SourceBill
		valid              bool
	}{
		{"paid retention", "reopen", "retain", &av.SourceBill{Status: "completed"}, true},
		{"paid cancellation needs refund", "cancel", "retain", &av.SourceBill{Status: "completed"}, false},
		{"wrong confirmation", "reverse_completion", "confirmation_error", &av.SourceBill{Status: "completed"}, true},
		{"submitted is not receipt", "cancel", "refunded", &av.SourceBill{Status: "submitted"}, false},
		{"submitted verified absent", "cancel", "no_payment_received", &av.SourceBill{Status: "submitted"}, true},
		{
			"reversed funding cannot refund",
			"reopen",
			"refunded",
			&av.SourceBill{Status: "closed", ConfirmationReversed: true},
			false,
		},
		{
			"refunded funding cannot retain",
			"reopen",
			"retain",
			&av.SourceBill{Status: "closed", RefundRegistered: true},
			false,
		},
		{
			"reversed funding restarts unpaid",
			"reopen",
			"no_payment_received",
			&av.SourceBill{Status: "closed", ConfirmationReversed: true},
			true,
		},
		{"stale lifecycle", "reopen", "no_payment_received", &av.SourceBill{Status: "closed", Superseded: true}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if e := ValidateSourceFunds(c.action, c.mode, []*av.SourceBill{c.bill}); (e == nil) != c.valid {
				t.Fatalf("valid=%v error=%v", c.valid, e)
			}
		})
	}
}

func TestMixedSourceFundsRequireExplicitSubmittedVerification(t *testing.T) {
	bills := []*av.SourceBill{{Status: "completed"}, {Status: "submitted"}}
	if e := ValidateSourceFunds("cancel", "refunded", bills); e == nil {
		t.Fatal("mixed funds accepted without verification")
	}
	if e := ValidateSourceFunds("cancel", "refunded", bills, true); e != nil {
		t.Fatal(e)
	}
	if e := ValidateSourceFunds("cancel", "refunded", bills, false); e == nil {
		t.Fatal("false verification accepted")
	}
}
