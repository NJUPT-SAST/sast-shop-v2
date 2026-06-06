package interceptor

import (
	"context"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
)

// ValidationLogging returns a Connect unary interceptor that logs validation failures.
func ValidationLogging(logger zerolog.Logger) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			resp, err := next(ctx, req)
			if connect.CodeOf(err) == connect.CodeInvalidArgument {
				logger.Warn().Err(err).Msg("RPC request validation failed")
			}
			return resp, err
		}
	}
}
