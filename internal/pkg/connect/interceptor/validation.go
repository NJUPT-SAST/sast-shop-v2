package interceptor

import (
	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/rs/zerolog"
)

// NewValidationChain creates a Connect handler option that combines protovalidate
// with validation-failure logging. Microservices call this once and pass the result
// to every handler.
func NewValidationChain(logger zerolog.Logger) (connect.HandlerOption, error) {
	validator, err := protovalidate.New()
	if err != nil {
		return nil, err
	}
	return connect.WithInterceptors(
		ValidationLogging(logger),
		validate.NewInterceptor(validate.WithValidator(validator)),
	), nil
}
