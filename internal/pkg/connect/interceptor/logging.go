package interceptor

import (
	"context"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
)

// connect.UnaryInterceptorFunc，适配器类型，用于将普通函数转换为 Connect 的一元拦截器。要求返回一个函数，该函数接收 next connect.UnaryFunc（链中的下一个处理器），并返回一个新的 connect.UnaryFunc。
// ValidationLogging returns a Connect unary interceptor that logs validation failures.
func ValidationLogging(logger zerolog.Logger) connect.UnaryInterceptorFunc {
	// 接收 next connect.UnaryFunc
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		// 这是 Connect 一元 RPC 的标准处理函数。
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			// 该拦截器不执行验证，只负责在验证失败（或其他导致 CodeInvalidArgument 的错误）时记录日志。它通常与真正的验证拦截器配合使用。

			resp, err := next(ctx, req)
			if connect.CodeOf(err) == connect.CodeInvalidArgument {
				logger.Warn().
					Err(err).
					Str("procedure", req.Spec().Procedure).
					Str("peer", req.Peer().Addr). // 从 ctx 提取
					Msg("RPC request validation failed")
			}
			return resp, err
		}
	}
}
