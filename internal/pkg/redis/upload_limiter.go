package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

var ErrUploadLimiterUnavailable = errors.New("upload limiter unavailable")

var uploadLimitScript = goredis.NewScript(`
local count = redis.call("INCR", KEYS[1])
if count == 1 then
	redis.call("EXPIRE", KEYS[1], ARGV[1])
end
if count <= tonumber(ARGV[2]) then
	return 1
end
return 0
`)

type UploadLimiter struct {
	window time.Duration
	limit  int64
}

func NewUploadLimiter(window time.Duration, limit int64) *UploadLimiter {
	return &UploadLimiter{window: window, limit: limit}
}

func (l *UploadLimiter) Allow(ctx context.Context, userID int64) (bool, error) {
	// redis未初始化
	if Client == nil {
		return false, ErrUploadLimiterUnavailable
	}
	// 保证至少为一秒
	seconds := int64(l.window / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	key := fmt.Sprintf("upload:product-image:%d", userID)
	result, err := uploadLimitScript.Run(
		ctx,
		Client,
		[]string{key},
		seconds,
		l.limit,
	).Int64()
	if err != nil {
		return false, ErrUploadLimiterUnavailable
	}
	return result == 1, nil
}
