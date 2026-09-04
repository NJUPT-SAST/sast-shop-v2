package storage

import (
	"context"
	"io"
)

// 只要实现这5个方法，上传服务即可执行
type ObjectStore interface {
	Put(
		ctx context.Context,
		key string,
		body io.Reader,
		size int64,
		contentType string,
	) error

	Delete(ctx context.Context, key string) error
	PublicURL(key string) (string, error) // 生成公开访问链接
	// ProbePublic 必须仅在对象可以被公开获取后才返回。
	// 实现应验证响应的内容类型和状态。
	ProbePublic(ctx context.Context, url, contentType string) error
	Ready(ctx context.Context) error // 健康检查
}
