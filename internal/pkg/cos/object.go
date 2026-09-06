package cos

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/rs/zerolog/log"
	cossdk "github.com/tencentyun/cos-go-sdk-v5"
)

// closeBody 关闭响应体，失败只记警告（响应已完成，关闭失败无业务影响）。
func closeBody(closer io.Closer) {
	if closer == nil {
		return
	}
	if err := closer.Close(); err != nil {
		log.Warn().Err(err).Msg("cos: failed to close response body")
	}
}

// PutObject 将对象写入 COS，成功时对象立即可读。
func PutObject(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	client, err := getClient()
	if err != nil {
		return err
	}
	resp, err := client.SDK.Object.Put(ctx, key, body, &cossdk.ObjectPutOptions{
		ObjectPutHeaderOptions: &cossdk.ObjectPutHeaderOptions{
			ContentType:   contentType,
			ContentLength: size,
		},
	})
	if err != nil {
		return fmt.Errorf("cos: put object %s: %w", key, err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cos: put object %s: status=%d", key, resp.StatusCode)
	}
	return nil
}

// DeleteObject 删除对象。COS 删除不存在对象同样返回成功，因此天然幂等。
func DeleteObject(ctx context.Context, key string) error {
	client, err := getClient()
	if err != nil {
		return err
	}
	resp, err := client.SDK.Object.Delete(ctx, key)
	if err != nil {
		return fmt.Errorf("cos: delete object %s: %w", key, err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusNoContent &&
		resp.StatusCode != http.StatusOK &&
		resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("cos: delete object %s: status=%d", key, resp.StatusCode)
	}
	return nil
}

// PublicURL 拼接 CDN 基础地址与 key 生成公开访问链接。
// 不使用 SDK 的预签名 URL —— 签名 URL 内嵌凭据，违反对外 URL 要求。
func PublicURL(key string) (string, error) {
	client, err := getClient()
	if err != nil {
		return "", err
	}
	if client.CDNBaseURL == "" {
		return "", fmt.Errorf("cos: cdn base url is not configured")
	}
	return client.CDNBaseURL + "/" + key, nil
}

// ProbePublic GET 公开 URL，验证对象已可被公开访问且响应内容类型与预期一致。
func ProbePublic(ctx context.Context, publicURL, contentType string) error {
	client, err := getClient()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, publicURL, nil)
	if err != nil {
		return fmt.Errorf("cos: probe public url: %w", err)
	}
	resp, err := client.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("cos: probe public url: %w", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cos: probe public url: status=%d", resp.StatusCode)
	}
	got, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || got != contentType {
		return fmt.Errorf("cos: probe public url: content-type=%q want %q", got, contentType)
	}
	return nil
}

// Ready 通过 HeadBucket 检查桶可达且凭据有效，供 /health/ready 使用。
func Ready(ctx context.Context) error {
	client, err := getClient()
	if err != nil {
		return err
	}
	resp, err := client.SDK.Bucket.Head(ctx)
	if err != nil {
		return fmt.Errorf("cos: head bucket: %w", err)
	}
	defer closeBody(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cos: head bucket: status=%d", resp.StatusCode)
	}
	return nil
}
