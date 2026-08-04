package errmsg

import "connectrpc.com/connect"

// payment 服务场景错误枚举。
var (
	BillNotFound       = Biz{connect.CodeNotFound, "账单不存在"}
	InvalidBillStatus  = Biz{connect.CodeFailedPrecondition, "账单状态不允许该操作"}
	InvalidChannel     = Biz{connect.CodeInvalidArgument, "支付渠道不合法"}
	DuplicateBill      = Biz{connect.CodeFailedPrecondition, "账单已存在，请勿重复操作"}
	InvalidCreateBill  = Biz{connect.CodeInvalidArgument, "创建账单请求不合法"}
	PayerPayeeSame     = Biz{connect.CodeInvalidArgument, "付款方和收款方不能是同一人"}
	BillVersionConflict = Biz{connect.CodeAborted, "账单已被修改，请刷新后重试"}
)
