package feishu

import (
	"context"
	"fmt"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkaccesstoken "github.com/larksuite/oapi-sdk-go/v3/core/accesstoken"
	"github.com/larksuite/oapi-sdk-go/v3/core/accesstoken/authorizationcode"
	"github.com/larksuite/oapi-sdk-go/v3/core/accesstoken/refreshtoken"
)

// 授权码换token
func ExchangeCode(ctx context.Context, code string, codeVerifier string, redirectURI string) (*OAuthToken, error) {
	client, err := getClient()
	if err != nil {
		return nil, err
	}
	fmt.Printf("[DEBUG] 1. 传入的 redirectURI: %s\n", redirectURI)
	fmt.Printf("[DEBUG] 2. client.RedirectURL (SDK默认): %s\n", client.RedirectURL)

	reqBuilder := authorizationcode.NewTokenRequestBuilder().Code(code)
	redirect := redirectURI
	if redirect == "" {
		redirect = client.RedirectURL
	}
	if redirect != "" {
		reqBuilder.RedirectUri(redirect)
	}
	if codeVerifier != "" {
		reqBuilder.CodeVerifier(codeVerifier)
	}
	fmt.Printf("[DEBUG] 3. 最终发给飞书的 redirect_uri: %s\n", redirect)

	resp, err := client.SDK.AccessToken.RetrieveByAuthorizationCode(ctx, reqBuilder.Build())
	if err != nil {
		return nil, mapFeishuError(err)
	}
	if resp == nil {
		return nil, fmt.Errorf("feishu access token response is empty")
	}

	token := oauthTokenFromSDK(resp.Data)
	if token.AccessToken == "" {
		return nil, fmt.Errorf("feishu access token is empty")
	}
	return token, nil
}

// 刷新token
// 后续如果需要持续以用户身份调用飞书接口，再接入令牌保存和刷新机制，比如持续读取用户授权的飞书日历”：用户早上登录，下午后台仍需读取日历，此时就可能需要刷新飞书令牌，避免再次要求用户授权。刷新凭证也失效时，再走登录授权流程。函数本身只负责请求新令牌，保存结果需要调用方处理
func RefreshUserToken(ctx context.Context, refreshToken string) (*OAuthToken, error) {
	client, err := getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.SDK.AccessToken.Refresh(
		ctx, refreshtoken.NewTokenRequestBuilder().
			RefreshToken(refreshToken).
			Build(),
	)
	if err != nil {
		return nil, mapFeishuError(err)
	}
	if resp == nil {
		return nil, fmt.Errorf("feishu refresh token response is empty")
	}

	return oauthTokenFromSDK(resp.Data), nil
}

func GetCurrentUser(ctx context.Context, userAccessToken string) (*UserInfo, error) {
	client, err := getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.SDK.Authen.UserInfo.Get(ctx, larkcore.WithUserAccessToken(userAccessToken))
	if err != nil {
		return nil, mapFeishuError(err)
	}
	if resp == nil {
		return nil, fmt.Errorf("feishu user info response is empty")
	}
	if !resp.Success() {
		return nil, &APIError{
			Code:    resp.Code,
			Message: resp.Msg,
		}
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("feishu user info data is empty")
	}

	return &UserInfo{
		Name:            larkcore.StringValue(resp.Data.Name),
		AvatarURL:       larkcore.StringValue(resp.Data.AvatarUrl),
		OpenID:          larkcore.StringValue(resp.Data.OpenId),
		UnionID:         larkcore.StringValue(resp.Data.UnionId),
		Email:           larkcore.StringValue(resp.Data.Email),
		EnterpriseEmail: larkcore.StringValue(resp.Data.EnterpriseEmail),
		UserID:          larkcore.StringValue(resp.Data.UserId),
		TenantKey:       larkcore.StringValue(resp.Data.TenantKey),
		EmployeeNo:      larkcore.StringValue(resp.Data.EmployeeNo),
	}, nil
}

// 把飞书的AccessTokenRespData 转换成项目自定义的token，与SDK解耦
func oauthTokenFromSDK(data *larkaccesstoken.AccessTokenRespData) *OAuthToken {
	if data == nil {
		return &OAuthToken{}
	}

	return &OAuthToken{
		AccessToken:           larkcore.StringValue(data.AccessToken),
		ExpiresIn:             int32(larkcore.IntValue(data.ExpiresIn)), //nolint:gosec
		RefreshToken:          larkcore.StringValue(data.RefreshToken),
		RefreshTokenExpiresIn: int32(larkcore.IntValue(data.RefreshTokenExpiresIn)), //nolint:gosec
		TokenType:             larkcore.StringValue(data.TokenType),
		Scope:                 larkcore.StringValue(data.Scope),
	}
}
