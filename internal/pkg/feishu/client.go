package feishu

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkaccesstoken "github.com/larksuite/oapi-sdk-go/v3/core/accesstoken"
)

const (
	baseOpenAPIURL = "https://open.feishu.cn"
	baseAccountURL = "https://accounts.feishu.cn"
)

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

type Client struct {
	AppID       string
	AppSecret   string
	RedirectURL string
	SDK         *lark.Client
}

var AppClient *Client

func Init() {
	cfg := config.AppConfig
	AppClient = &Client{
		AppID:       cfg.Feishu_AppID,
		AppSecret:   cfg.Feishu_AppSecret,
		RedirectURL: cfg.Feishu_REDIRECT_URL,
		SDK:         newSDKClient(cfg.Feishu_AppID, cfg.Feishu_AppSecret),
	}
}

func newSDKClient(appID string, appSecret string) *lark.Client {
	return lark.NewClient(
		appID,
		appSecret,
		lark.WithOpenBaseUrl(baseOpenAPIURL),
		lark.WithOAuthBaseUrl(baseAccountURL),
		lark.WithReqTimeout(10*time.Second),
		lark.WithHttpClient(httpClient),
		lark.WithTokenCache(newSDKTokenCache()),
	)
}

func getClient() (*Client, error) {
	if AppClient == nil || AppClient.SDK == nil {
		return nil, fmt.Errorf("feishu client is not initialized")
	}
	return AppClient, nil
}

func GetTenantAccessToken(ctx context.Context) (string, error) {
	client, err := getClient()
	if err != nil {
		return "", err
	}

	resp, err := client.SDK.GetTenantAccessTokenBySelfBuiltApp(ctx, &larkcore.SelfBuiltTenantAccessTokenReq{
		AppID:     client.AppID,
		AppSecret: client.AppSecret,
	})
	if err != nil {
		return "", mapFeishuError(err)
	}
	if resp == nil {
		return "", fmt.Errorf("feishu tenant access token response is empty")
	}
	if !resp.Success() {
		return "", &APIError{
			Code:    resp.Code,
			Message: resp.Msg,
		}
	}

	_ = setCachedSelfBuiltTenantAccessToken(ctx, client.AppID, resp.TenantAccessToken, resp.Expire)
	return resp.TenantAccessToken, nil
}

func mapFeishuError(err error) error {
	if err == nil {
		return nil
	}

	var tokenErr *larkaccesstoken.AccessTokenError
	if errors.As(err, &tokenErr) {
		return &OAuthError{
			Code:             tokenErr.Code,
			ErrorCode:        tokenErr.ErrorType,
			ErrorDescription: tokenErr.ErrorDescription,
		}
	}

	var codeErr larkcore.CodeError
	if errors.As(err, &codeErr) {
		return &APIError{
			Code:    codeErr.Code,
			Message: codeErr.Msg,
		}
	}

	return err
}
