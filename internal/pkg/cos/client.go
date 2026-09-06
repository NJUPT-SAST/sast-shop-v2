package cos

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
	cossdk "github.com/tencentyun/cos-go-sdk-v5"
)

type Client struct {
	SecretID   string
	SecretKey  string
	BucketURL  *url.URL
	CDNBaseURL string
	SDK        *cossdk.Client
	// HTTP 是不带签名逻辑的普通客户端，ProbePublic 用它访问 CDN 公开地址，
	// 确保探测请求不会携带任何存储凭据。
	HTTP *http.Client
}

var AppClient *Client

// Init 读取全局配置初始化 COS 客户端。
// 凭据缺失或为占位符时：development 静默跳过（AppClient 保持 nil），production panic。
func Init() {
	cfg := config.AppConfig
	if cfg.Cos_SecretID == "" || cfg.Cos_SecretKey == "" ||
		cfg.Cos_SecretID == constant.CosDefaultSecretID || cfg.Cos_SecretKey == constant.CosDefaultSecretKey ||
		cfg.Cos_BucketURL == "" || cfg.Cos_CDNBaseURL == "" {
		if cfg.AppEnv == config.Development {
			return
		}
		panic("cos: COS_SECRET_ID / COS_SECRET_KEY / COS_BUCKET_URL / COS_CDN_BASE_URL " +
			"must be configured with real credentials")
	}
	bucketURL, err := url.Parse(cfg.Cos_BucketURL)
	if err != nil || bucketURL.Scheme != "https" || bucketURL.Host == "" {
		panic("cos: COS_BUCKET_URL must be a valid https URL")
	}
	cdnBase := strings.TrimRight(cfg.Cos_CDNBaseURL, "/")
	if parsed, err := url.Parse(cdnBase); err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		panic("cos: COS_CDN_BASE_URL must be a valid https URL")
	}
	AppClient = &Client{
		SecretID:   cfg.Cos_SecretID,
		SecretKey:  cfg.Cos_SecretKey,
		BucketURL:  bucketURL,
		CDNBaseURL: cdnBase,
		HTTP:       &http.Client{Timeout: 10 * time.Second},
		SDK: cossdk.NewClient(&cossdk.BaseURL{BucketURL: bucketURL}, &http.Client{
			Timeout: 30 * time.Second,
			Transport: &cossdk.AuthorizationTransport{
				SecretID:  cfg.Cos_SecretID,
				SecretKey: cfg.Cos_SecretKey,
			},
		}),
	}
}

func getClient() (*Client, error) {
	if AppClient == nil || AppClient.SDK == nil {
		return nil, fmt.Errorf("cos client is not initialized")
	}
	return AppClient, nil
}
