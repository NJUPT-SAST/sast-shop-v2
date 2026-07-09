package redis

import (
	"context"
	"fmt"
	"strings"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
	"github.com/redis/go-redis/v9"
)

var Client *redis.Client

func Init(serviceName string) {
	cfg := config.AppConfig
	Client = redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Redis_Host, cfg.Redis_Port),
		Password: cfg.Redis_Password,
		DB:       cfg.Redis_DB,
	})
	Client.AddHook(&prefixHook{
		projectPrefix: constant.ProjectName,
		servicePrefix: serviceName,
	})

	if err := Client.Ping(context.Background()).Err(); err != nil {
		panic(fmt.Sprintf("failed to connect to redis: %v", err))
	}
}

// prefixHook is a Redis hook that adds a prefix to all keys in commands and pipelines.
type prefixHook struct {
	projectPrefix string
	servicePrefix string
}

func (h *prefixHook) fullPrefix() string {
	return fmt.Sprintf("%s:%s", h.projectPrefix, h.servicePrefix)
}

func (h *prefixHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *prefixHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		var prefix string
		if shouldSkipServicePrefix(ctx) {
			prefix = h.projectPrefix + ":"
		} else {
			prefix = h.fullPrefix() + ":"
		}
		args := cmd.Args()
		if len(args) > 1 {
			if key, ok := args[1].(string); ok && !strings.HasPrefix(key, h.fullPrefix()) {
				args[1] = prefix + key
			}
		}
		return next(ctx, cmd)
	}
}

func (h *prefixHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		var prefix string
		if shouldSkipServicePrefix(ctx) {
			prefix = h.projectPrefix + ":"
		} else {
			prefix = h.fullPrefix() + ":"
		}

		for _, cmd := range cmds {
			args := cmd.Args()
			if len(args) > 1 {
				if key, ok := args[1].(string); ok && !strings.HasPrefix(key, h.fullPrefix()) {
					args[1] = prefix + key
				}
			}
		}
		return next(ctx, cmds)
	}
}

type ctxKey struct{}

var skipServicePrefixKey ctxKey

func WithProjectPrefixOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipServicePrefixKey, true)
}

func shouldSkipServicePrefix(ctx context.Context) bool {
	val, ok := ctx.Value(skipServicePrefixKey).(bool)
	return ok && val
}
