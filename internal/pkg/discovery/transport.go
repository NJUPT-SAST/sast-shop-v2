package discovery

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/hashicorp/consul/api"
	"github.com/rs/zerolog/log"
)

type Transport struct {
	ConsulClient *api.Client       // 向consul发起健康查询的sdk客户端
	ServiceName  string            // 逻辑服务名
	Base         http.RoundTripper //发送者，只负责解析和改写地址
}

// 执行单次http事务
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// 通过consul拿到一个健康实例的url
	RawUrl, err := Resolve(req.Context(), t.ConsulClient, t.ServiceName)
	if err != nil {
		closeBody(req)
		return nil, fmt.Errorf("resolve %s: %w", t.ServiceName, err)
	}
	// 把 URL 字符串解析成 url.URL 结构，提取 Scheme 和 Host。
	u, err := url.Parse(RawUrl)
	if err != nil {
		closeBody(req)
		return nil, fmt.Errorf("parse resolved url %q: %w", RawUrl, err)
	}
	// 在不修改原始请求的前提下，把目标地址替换为解析出的真实地址。
	clone := req.Clone(req.Context())
	clone.URL.Scheme = u.Scheme
	clone.URL.Host = u.Host
	log.Printf("[discovery] %s -> %s%s", req.URL.Host, u.Host, req.URL.Path)
	// 由底层传输层完成实际的网络通信。
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

func closeBody(req *http.Request) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
}
