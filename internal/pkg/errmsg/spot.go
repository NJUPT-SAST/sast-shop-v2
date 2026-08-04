package errmsg

import "connectrpc.com/connect"

// spot 服务场景错误枚举。
var (
	NotFound                    = Biz{connect.CodeNotFound, "请求的资源不存在"}
	SpotGoodsNotFound           = Biz{connect.CodeNotFound, "商品不存在"}
	SpotOrderNotFound            = Biz{connect.CodeNotFound, "订单不存在"}
	SpotGoodsClosed              = Biz{connect.CodeFailedPrecondition, "商品已下架"}
	SpotInsufficientStock        = Biz{connect.CodeFailedPrecondition, "库存不足"}
	SpotCannotPurchaseOwnGoods   = Biz{connect.CodeFailedPrecondition, "不能购买自己发布的商品"}
	SpotPermissionDenied         = Biz{connect.CodePermissionDenied, "没有权限执行该操作"}
	SpotOrderInvalidStatus       = Biz{connect.CodeFailedPrecondition, "订单状态不允许该操作"}
	SpotSellerContactUnavailable = Biz{connect.CodeFailedPrecondition, "卖家暂未提供联系方式"}
	SpotGoodsVersionConflict     = Biz{connect.CodeAborted, "商品已被修改，请刷新后重试"}
	SpotOrderVersionConflict     = Biz{connect.CodeAborted, "订单已被修改，请刷新后重试"}
	SpotInvalidCreateOrders      = Biz{connect.CodeInvalidArgument, "下单请求不合法"}
	SpotInvalidSellerContactReq  = Biz{connect.CodeInvalidArgument, "联系方式请求不合法"}
)
