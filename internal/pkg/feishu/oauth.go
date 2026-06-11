package feishu

import (
	"context"
	"fmt"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkaccesstoken "github.com/larksuite/oapi-sdk-go/v3/core/accesstoken"
	"github.com/larksuite/oapi-sdk-go/v3/core/accesstoken/authorizationcode"
	"github.com/larksuite/oapi-sdk-go/v3/core/accesstoken/refreshtoken"
)

func ExchangeCode(ctx context.Context, code string, codeVerifier string, redirectURI string) (*OAuthToken, error) {
	client, err := getClient()
	if err != nil {
		return nil, err
	}

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

func RefreshUserToken(ctx context.Context, refreshToken string) (*OAuthToken, error) {
	client, err := getClient()
	if err != nil {
		return nil, err
	}

	resp, err := client.SDK.AccessToken.Refresh(ctx, refreshtoken.NewTokenRequestBuilder().
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

	resp, err := client.SDK.Authen.V1.UserInfo.Get(ctx, larkcore.WithUserAccessToken(userAccessToken))
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

func oauthTokenFromSDK(data *larkaccesstoken.AccessTokenRespData) *OAuthToken {
	if data == nil {
		return &OAuthToken{}
	}

	return &OAuthToken{
		AccessToken:           larkcore.StringValue(data.AccessToken),
		ExpiresIn:             int32(larkcore.IntValue(data.ExpiresIn)),
		RefreshToken:          larkcore.StringValue(data.RefreshToken),
		RefreshTokenExpiresIn: int32(larkcore.IntValue(data.RefreshTokenExpiresIn)),
		TokenType:             larkcore.StringValue(data.TokenType),
		Scope:                 larkcore.StringValue(data.Scope),
	}
}
