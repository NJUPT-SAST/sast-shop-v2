package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

var ErrUploadQuotaUnavailable = errors.New("upload quota unavailable")

// 每日配额：key 按用户 + 日期隔离，48 小时过期覆盖跨日残留。
const uploadQuotaKeyTTL = 48 * time.Hour

// allow 只做查询，不修改计数；commit 在存储成功后累加字节数。
var uploadQuotaAllowScript = goredis.NewScript(`
local count = tonumber(redis.call("GET", KEYS[1]) or "0")
if count + tonumber(ARGV[1]) <= tonumber(ARGV[2]) then
	return 1
end
return 0
`)

var uploadQuotaCommitScript = goredis.NewScript(`
local count = redis.call("INCRBY", KEYS[1], ARGV[1])
if redis.call("TTL", KEYS[1]) < 0 then
	redis.call("EXPIRE", KEYS[1], ARGV[2])
end
return count
`)

type UploadQuota struct {
	limitBytes int64
}

func NewUploadQuota(limitBytes int64) *UploadQuota {
	return &UploadQuota{limitBytes: limitBytes}
}

// Allow 判断 userID 当前日累计字节数加上 size 后是否仍不超配额。
func (q *UploadQuota) Allow(ctx context.Context, userID, size int64) (bool, error) {
	// redis未初始化
	if Client == nil {
		return false, ErrUploadQuotaUnavailable
	}
	if size <= 0 {
		return true, nil
	}
	key := uploadQuotaKey(userID)
	result, err := uploadQuotaAllowScript.Run(
		ctx,
		Client,
		[]string{key},
		size,
		q.limitBytes,
	).Int64()
	if err != nil {
		return false, ErrUploadQuotaUnavailable
	}
	return result == 1, nil
}

// Commit 在对象存储成功后累加字节数。
func (q *UploadQuota) Commit(ctx context.Context, userID, size int64) error {
	// redis未初始化
	if Client == nil {
		return ErrUploadQuotaUnavailable
	}
	if size <= 0 {
		return nil
	}
	key := uploadQuotaKey(userID)
	_, err := uploadQuotaCommitScript.Run(
		ctx,
		Client,
		[]string{key},
		size,
		int64(uploadQuotaKeyTTL/time.Second),
	).Result()
	if err != nil {
		return ErrUploadQuotaUnavailable
	}
	return nil
}

func uploadQuotaKey(userID int64) string {
	return fmt.Sprintf("upload:product-image:quota:%d:%s", userID, time.Now().Format("20060102"))
}
