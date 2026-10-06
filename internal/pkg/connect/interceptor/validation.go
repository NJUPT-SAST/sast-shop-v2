package interceptor

import (
	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/rs/zerolog"
)

// 创建一个可用于connect服务的配置选项，该选项添加 日志记录和 protobuf消息验证
// NewValidationChain creates a Connect handler option that combines protovalidate
// with validation-failure logging. Microservices call this once and pass the result
// to every handler.
func NewValidationChain(logger zerolog.Logger) (connect.HandlerOption, error) {
	validator, err := protovalidate.New()
	if err != nil {
		return nil, err
	}
	// 接收多个拦截器，并将这些拦截器应用到服务处理器上
	return connect.WithInterceptors(
		ValidationLogging(logger),
		validate.NewInterceptor(validate.WithValidator(validator)),
	), nil
}
