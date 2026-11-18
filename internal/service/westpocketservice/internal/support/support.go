package support

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"

	"connectrpc.com/connect"
	"github.com/rs/zerolog/log"
	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
)

func ObserveError(err error) {
	if err != nil {
		log.Warn().Str("code", SafeError(err)).Msg("West Pocket background operation failed")
	}
}
func CloseResource(resource io.Closer) { ObserveError(resource.Close()) }

// BoundedInt32 is used for already constrained image dimensions, counts and pagination.
func BoundedInt32(v int) int32 {
	if v < 0 {
		return 0
	}
	if v > 2147483647 {
		return 2147483647
	}
	return int32(v)
}

func Failure(code connect.Code, message string) error {
	return connect.NewError(code, errors.New(message))
}

func RPCError(e error) error {
	if e == nil {
		return nil
	}
	var c *connect.Error
	if errors.As(e, &c) {
		return e
	}
	if errors.Is(e, sql.ErrNoRows) {
		return Failure(connect.CodeNotFound, "资源不存在")
	}
	return Failure(connect.CodeInternal, "暂时无法完成操作，请稍后重试")
}

// 将错误转换为安全的错误码字符串。腾讯云 SDK 错误转为 PROVIDER_<Code>；已知业务错误原样返回；超时转为 PROVIDER_TIMEOUT；其他错误转为 DEPENDENCY_<connect.Code>，避免向外泄露底层细节。
func SafeError(err error) string {
	if err == nil {
		return ""
	}
	var api *tcerr.TencentCloudSDKError
	if errors.As(err, &api) {
		return "PROVIDER_" + api.Code
	}
	for _, code := range []string{"NO_FACE", "LOW_QUALITY", "ONE_FACE_REQUIRED", "FACE_LIMIT_REACHED", "SAMPLE_REJECTED"} {
		if err.Error() == code {
			return code
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "PROVIDER_TIMEOUT"
	}
	return fmt.Sprintf("DEPENDENCY_%s", connect.CodeOf(err).String())
}

func Value[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
