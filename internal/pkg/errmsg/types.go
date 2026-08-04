// Package errmsg 集中定义各服务场景错误的 connect 语义与用户友好文案。
// 实现处引用枚举，不写死字符串。
package errmsg

import "connectrpc.com/connect"

// Biz 场景错误：connect 语义 + 用户文案
type Biz struct {
	Code connect.Code
	Msg  string
}

// Error 实现 error 接口，可直接作为 connect.NewError 的 err 参数。
func (b Biz) Error() string { return b.Msg }

