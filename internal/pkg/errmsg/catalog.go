package errmsg

import "connectrpc.com/connect"

// catalog 服务场景错误枚举。
var (
	ProductTemplateNotFound  = Biz{connect.CodeNotFound, "商品模板不存在"}
	StoreNotFound            = Biz{connect.CodeNotFound, "店铺不存在"}
	BarcodeNotFound          = Biz{connect.CodeNotFound, "条码不存在"}
	ProductTemplateForbidden = Biz{connect.CodePermissionDenied, "只能删除自己创建的模板"}
	AdminOnly                = Biz{connect.CodePermissionDenied, "仅管理员可执行该操作"}
)
