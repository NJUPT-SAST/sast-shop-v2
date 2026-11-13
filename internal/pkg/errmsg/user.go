package errmsg

import "connectrpc.com/connect"

// user 服务场景错误枚举。
var (
	AddressNotFound = Biz{connect.CodeNotFound, "地址不存在"}
	UserNotFound    = Biz{connect.CodeNotFound, "用户不存在"}
)
