package admin

import (
	"errors"

	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
)

// ValidateSourceFunds rejects impossible funding assertions before a source is reserved.
// Payment repeats these checks inside its transaction against authoritative funding history.
//
//nolint:gocyclo // Keep the funding-state decision table together for review against Payment.
func ValidateSourceFunds(action, mode string, bills []*av.SourceBill, submittedUnreceived ...bool) error {
	verifiedUnreceived := len(submittedUnreceived) > 0 && submittedUnreceived[0]
	switch mode {
	case "no_payment_received", "retain", "refunded", "confirmation_error":
	default:
		return connect.NewError(connect.CodeInvalidArgument, errors.New("请确认原付款处理方式"))
	}
	for _, bill := range bills {
		message := ""
		switch {
		case bill.Superseded:
			message = "账单已被其他处理轮次取代，请刷新后重新预览"
		case bill.Status == "submitted" && mode != "no_payment_received" && !verifiedUnreceived:
			message = "已申报付款须先逐笔核实未到账，并明确勾选核实确认；实际到账请先确认收款"
		case bill.Status == "completed" && mode == "no_payment_received":
			message = "已确认收款须选择保留、已核验退款或原确认有误"
		case bill.Status == "completed" && action == "cancel" && mode != "refunded" && mode != "confirmation_error":
			message = "取消已收款交易须核验退款或确认原收款有误"
		case bill.ConfirmationReversed && (mode == "retain" || mode == "refunded"):
			message = "已撤销收款确认的款项不能保留付款或登记退款"
		case bill.RefundRegistered && (mode == "retain" || mode == "confirmation_error"):
			message = "已退款的款项不能保留付款或登记为从未收款"
		}
		if message != "" {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New(message))
		}
	}
	return nil
}
