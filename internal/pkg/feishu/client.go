package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// baseURL 是飞书开放平台 API 的根地址，后续请求通过 baseURL + 路径 拼接完整 URL。
const baseURL = "https://open.feishu.cn/open-apis"

// Client 封装调用飞书开放平台 API 的 HTTP 客户端。
type Client struct {
	httpClient *http.Client
}

// DefaultClient 是包级共享的飞书客户端单例。
var DefaultClient = NewClient()

// NewClient 返回一个底层 http.Client 超时为 10 秒的飞书客户端。
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// apiResponse 是飞书开放平台 API 的通用响应结构，code 为 0 表示成功。
type apiResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// postJSON 向飞书发送 JSON 格式的 POST 请求，并统一处理错误。
// headers 为额外请求头（如 Authorization），body 会被序列化为 JSON 请求体；
// 当 out 不为 nil 时，成功响应的 data 字段会被反序列化到 out。
// 若 HTTP 状态码 >= 400 或飞书返回的 code != 0，则返回错误。
func (c *Client) postJSON(
	ctx context.Context,
	url string,
	headers map[string]string,
	body any,
	out any,
) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("feishu http %d: %s", resp.StatusCode, string(raw))
	}

	var result apiResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.Code != 0 {
		return fmt.Errorf("feishu api error %d: %s", result.Code, result.Msg)
	}
	if out != nil && len(result.Data) > 0 {
		if err := json.Unmarshal(result.Data, out); err != nil {
			return err
		}
	}
	return nil
}
