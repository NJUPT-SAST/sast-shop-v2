package feishu

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkaccesstoken "github.com/larksuite/oapi-sdk-go/v3/core/accesstoken"
)

type Client struct {
	AppID       string
	AppSecret   string
	RedirectURL string
	SDK         *lark.Client
}

var AppClient *Client

func Init() {
	cfg := config.AppConfig
	if cfg.Feishu_AppID == "" || cfg.Feishu_AppSecret == "" ||
		cfg.Feishu_AppID == constant.FeishuDefaultAppID || cfg.Feishu_AppSecret == constant.FeishuDefaultAppSecret {
		panic("feishu: FEISHU_APP_ID / FEISHU_APP_SECRET must be configured with real credentials")
	}
	AppClient = &Client{
		AppID:       cfg.Feishu_AppID,
		AppSecret:   cfg.Feishu_AppSecret,
		RedirectURL: cfg.Feishu_REDIRECT_URL,
		SDK: lark.NewClient(
			cfg.Feishu_AppID,
			cfg.Feishu_AppSecret,
			lark.WithOpenBaseUrl(constant.FeishuOpenAPIBaseURL),
			lark.WithOAuthBaseUrl(constant.FeishuAccountBaseURL),
			lark.WithReqTimeout(10*time.Second),
			lark.WithHttpClient(http.DefaultClient),
			lark.WithTokenCache(newSDKTokenCache()),
		),
	}
}

func getClient() (*Client, error) {
	if AppClient == nil || AppClient.SDK == nil {
		return nil, fmt.Errorf("feishu client is not initialized")
	}
	return AppClient, nil
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
