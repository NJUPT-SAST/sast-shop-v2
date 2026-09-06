package storage

import (
	"context"
	"errors"
	"io"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/cos"
)

// CosStore 将 pkg/cos 全局客户端适配为 ObjectStore 接口。
type CosStore struct{}

// NewCosStore 在 COS 客户端未初始化（如开发环境未配置凭据）时返回错误，
// 调用方据此优雅降级：不装配 Uploader，上传接口返回 503 UPLOAD_UNAVAILABLE。
func NewCosStore() (*CosStore, error) {
	if cos.AppClient == nil {
		return nil, errors.New("cos client is not initialized")
	}
	return &CosStore{}, nil
}

func (s *CosStore) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	return cos.PutObject(ctx, key, body, size, contentType)
}

func (s *CosStore) Delete(ctx context.Context, key string) error {
	return cos.DeleteObject(ctx, key)
}

func (s *CosStore) PublicURL(key string) (string, error) {
	return cos.PublicURL(key)
}

func (s *CosStore) ProbePublic(ctx context.Context, url, contentType string) error {
	return cos.ProbePublic(ctx, url, contentType)
}

func (s *CosStore) Ready(ctx context.Context) error {
	return cos.Ready(ctx)
}
