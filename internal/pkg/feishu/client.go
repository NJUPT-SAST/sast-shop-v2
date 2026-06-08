package feishu

import (
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
