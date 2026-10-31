package redis

import (
	"context"
	"fmt"
	"strconv"
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
		h.prefixKeys(ctx, cmd)
		return next(ctx, cmd)
	}
}

func (h *prefixHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			h.prefixKeys(ctx, cmd)
		}
		return next(ctx, cmds)
	}
}

func (h *prefixHook) prefixKeys(ctx context.Context, cmd redis.Cmder) {
	args := cmd.Args()
	if len(args) < 2 {
		return
	}
	prefix := h.fullPrefix() + ":"
	if shouldSkipServicePrefix(ctx) {
		prefix = h.projectPrefix + ":"
	}
	first, end, step := 1, 2, 1
	switch strings.ToLower(cmd.Name()) {
	case "eval", "evalsha", "eval_ro", "evalsha_ro", "fcall", "fcall_ro":
		// Script source/hash comes first; only the declared KEYS receive a
		// namespace. ARGV and keyless scripts must remain untouched.
		if len(args) < 3 {
			return
		}
		count, err := strconv.Atoi(fmt.Sprint(args[2]))
		if err != nil || count < 0 || count > len(args)-3 {
			return
		}
		first, end = 3, 3+count
	case "del", "unlink", "exists", "touch", "mget":
		end = len(args)
	case "mset", "msetnx":
		end, step = len(args), 2
	case "auth", "hello", "client", "select", "ping", "echo", "quit", "command",
		"script", "function", "info", "config", "acl", "cluster", "sentinel",
		"multi", "exec", "discard", "unwatch", "pubsub", "subscribe", "unsubscribe",
		"psubscribe", "punsubscribe", "publish":
		// These commands have no key at argument 1. In particular, go-redis
		// issues HELLO/AUTH/CLIENT during connection initialization.
		return
	}
	for i := first; i < end; i += step {
		if key, ok := args[i].(string); ok && !strings.HasPrefix(key, prefix) {
			args[i] = prefix + key
		}
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
