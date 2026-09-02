package errmsg

import "connectrpc.com/connect"

// errand 服务场景错误枚举。
var (
	// 通用
	Internal         = Biz{connect.CodeInternal, "系统繁忙，请稍后再试"}
	InvalidArgument  = Biz{connect.CodeInvalidArgument, "请求参数不合法"}
	Unauthenticated  = Biz{connect.CodeUnauthenticated, "未登录或登录已过期，请重新登录"}
	PermissionDenied = Biz{connect.CodePermissionDenied, "没有权限执行该操作"}
	Concurrency      = Biz{connect.CodeAborted, "操作冲突，请刷新后重试"}

	// 资源不存在
	TaskNotFound                 = Biz{connect.CodeNotFound, "任务不存在"}
	TaskItemNotFound             = Biz{connect.CodeNotFound, "商品不存在"}
	AssignmentNotFound           = Biz{connect.CodeNotFound, "分发明细不存在"}
	DemandNotFound               = Biz{connect.CodeNotFound, "需求不存在"}
	BuyerErrandOrderNotFound     = Biz{connect.CodeNotFound, "跑腿订单不存在"}
	PaymentAssignmentNotFound    = Biz{connect.CodeNotFound, "账单关联不存在"}

	// 状态不允许
	TaskNotInShopping           = Biz{connect.CodeFailedPrecondition, "任务不在采购中状态"}
	TaskNotInDistributing       = Biz{connect.CodeFailedPrecondition, "任务不在分发中状态"}
	TaskNotInCollectingPayment  = Biz{connect.CodeFailedPrecondition, "任务不在收款中状态"}
	TaskNotInDistributingFlow   = Biz{connect.CodeFailedPrecondition, "任务不在分发流程中"}
	TaskCannotBeCancelled       = Biz{connect.CodeFailedPrecondition, "任务已完成或已取消，无法取消"}
	TaskItemNotHandled          = Biz{connect.CodeFailedPrecondition, "商品尚未处理"}
	TaskItemNotPurchased        = Biz{connect.CodeFailedPrecondition, "商品尚未采购"}
	TaskHasNoShoppingItems      = Biz{connect.CodeFailedPrecondition, "任务没有待采购商品"}
	TaskHasNoDistributingItems  = Biz{connect.CodeFailedPrecondition, "任务没有待分发商品"}
	UnhandledShoppingItems      = Biz{connect.CodeFailedPrecondition, "存在未处理的采购商品"}
	UnpricedDistributingItems   = Biz{connect.CodeFailedPrecondition, "存在未定价的采购商品"}
	UnassignedDistributingItems = Biz{connect.CodeFailedPrecondition, "存在未处理的分发明细"}
	IncompleteDistributingItems = Biz{connect.CodeFailedPrecondition, "存在分配未完成的商品"}
	TaskPaymentsNotCompleted    = Biz{connect.CodeFailedPrecondition, "仍有账单未完成支付"}
	DemandNotOpen               = Biz{connect.CodeFailedPrecondition, "需求已被接单或已结束"}
	CaptainUnavailable          = Biz{connect.CodeFailedPrecondition, "团长暂未接单"}

	// 参数不合法
	InvalidErrandTaskID                    = Biz{connect.CodeInvalidArgument, "任务 ID 不合法"}
	InvalidStoreID                         = Biz{connect.CodeInvalidArgument, "店铺 ID 不合法"}
	InvalidSaveShoppingTaskItemRequest     = Biz{connect.CodeInvalidArgument, "采购商品请求不合法"}
	InvalidTransitionRequest               = Biz{connect.CodeInvalidArgument, "状态流转请求不合法"}
	InvalidCancelTaskRequest               = Biz{connect.CodeInvalidArgument, "取消任务请求不合法"}
	InvalidUpdateActualPriceRequest        = Biz{connect.CodeInvalidArgument, "改价请求不合法"}
	InvalidPaymentConfirmedRequest         = Biz{connect.CodeInvalidArgument, "支付确认请求不合法"}
	InvalidAllPaymentsConfirmedRequest     = Biz{connect.CodeInvalidArgument, "收款确认请求不合法"}
	InvalidPurchasedQuantity               = Biz{connect.CodeInvalidArgument, "购买数量不合法"}
	InvalidDistributingAssignmentRequest   = Biz{connect.CodeInvalidArgument, "分发明细请求不合法"}
	InvalidTransitionToDistributingRequest = Biz{connect.CodeInvalidArgument, "开始分发请求不合法"}
	TaskNotInPendingDistributing           = Biz{connect.CodeFailedPrecondition, "任务不在待分发状态"}
	FullyUnpurchasedItemMustUseZeroPrice   = Biz{connect.CodeInvalidArgument, "未采购的商品价格必须为 0"}
	DistributedQuantityExceedsDemand       = Biz{connect.CodeInvalidArgument, "分配数量超过需求量"}
	DistributedQuantityExceedsPurchased    = Biz{connect.CodeFailedPrecondition, "分配数量超过采购数量"}
	InvalidErrandTaskStatus                = Biz{connect.CodeInvalidArgument, "任务状态参数不合法"}
	InvalidBuyerErrandOrderContactRequest  = Biz{connect.CodeInvalidArgument, "联系信息请求不合法"}
	InvalidUpdateErrandDemandRequest       = Biz{connect.CodeInvalidArgument, "修改需求请求不合法"}
	InvalidDemandItems                     = Biz{connect.CodeInvalidArgument, "需求商品不合法"}
	DuplicateProduct                       = Biz{connect.CodeInvalidArgument, "商品重复，请勿重复添加"}
	InvalidQuantity                        = Biz{connect.CodeInvalidArgument, "需求数量必须大于 0"}
	InvalidDeadline                        = Biz{connect.CodeInvalidArgument, "期望送达时间必须在未来"}
	EmptyDemandItems                       = Biz{connect.CodeInvalidArgument, "需求商品不能为空"}
	ProductInvalid                         = Biz{connect.CodeInvalidArgument, "商品不存在或已更新，请刷新后重试"}
	AmountExceedsInt32                     = Biz{connect.CodeInvalidArgument, "金额超出范围"}
	ValueExceedsInt32                      = Biz{connect.CodeInvalidArgument, "数值超出范围"}

	// 权限
	BuyerErrandOrderPermissionDenied = Biz{connect.CodePermissionDenied, "无权查看该订单"}
)
