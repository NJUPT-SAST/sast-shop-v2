package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	rpcinterceptor "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/feishu"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/redis"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/repository"
)

// 生成后端自有 access_token，32 字节随机 hex
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// 构造存入 Redis 的 AuthUser，AccessToken 存飞书 user_access_token
func buildAuthUser(u *model.UserAccount, feishuAccessToken string) *rpcinterceptor.AuthUser {
	return &rpcinterceptor.AuthUser{
		UserID:      u.ID,
		Role:        string(u.Role),
		Status:      string(u.Status),
		AccessToken: feishuAccessToken,
	}
}

// 账号状态门禁，restricted/banned/deleted 拒绝登录
func checkUserCanLogin(u *model.UserAccount) error {
	switch u.Status {
	case model.MemberStatusRestricted,
		model.MemberStatusBanned,
		model.MemberStatusDeleted:
		return rpcerror.NewInternalError(&commonv1.BusinessError_UserError{
			UserError: &userv1.UserError{
				Code: userv1.UserErrorCode_USER_ERROR_CODE_INTERNAL_ERROR,
			},
		}, fmt.Sprintf("user account is %s", u.Status))
	}
	return nil
}

// UserAccount → proto LoginMember 映射
func toLoginMember(u *model.UserAccount) *userv1.LoginMember {
	return &userv1.LoginMember{
		Id:          u.ID,
		DisplayName: u.DisplayName,
		AvatarUrl:   u.AvatarURL,
		Role:        string(u.Role),
		Status:      string(u.Status),
	}
}

// 核心登录流水线：ExchangeCode→GetCurrentUser→Upsert→生成 token→写 Redis→返回
func Login(ctx context.Context, req *userv1.LoginRequest) (*userv1.LoginResponse, error) {
	userError := func(msg string) error {
		return rpcerror.NewInternalError(&commonv1.BusinessError_UserError{
			UserError: &userv1.UserError{
				Code: userv1.UserErrorCode_USER_ERROR_CODE_INTERNAL_ERROR,
			},
		}, msg)
	}

	feishuToken, err := feishu.ExchangeCode(ctx, req.Code, "", req.GetRedirectUri())
	if err != nil {
		return nil, userError(fmt.Sprintf("feishu exchange code: %v", err))
	}

	userInfo, err := feishu.GetCurrentUser(ctx, feishuToken.AccessToken)
	if err != nil {
		return nil, userError(fmt.Sprintf("feishu get current user: %v", err))
	}

	user, err := repository.UpsertUser(ctx, userInfo.OpenID, userInfo.Name, userInfo.AvatarURL)
	if err != nil {
		return nil, userError(fmt.Sprintf("upsert user: %v", err))
	}

	if err := checkUserCanLogin(user); err != nil {
		return nil, err
	}

	sessionToken, err := generateToken()
	if err != nil {
		return nil, userError(fmt.Sprintf("generate token: %v", err))
	}

	authUser := buildAuthUser(user, feishuToken.AccessToken)
	store := redis.NewSessionStore()
	if err := store.SaveSession(ctx, sessionToken, authUser); err != nil {
		return nil, userError(fmt.Sprintf("save session: %v", err))
	}
	if err := store.SaveUserCache(ctx, authUser); err != nil {
		return nil, userError(fmt.Sprintf("save user cache: %v", err))
	}

	return &userv1.LoginResponse{
		AccessToken: sessionToken,
		ExpiresIn:   int32(constant.SessionTTL.Seconds()),
		Member:      toLoginMember(user),
	}, nil
}

// 前端 h5sdk.config 鉴权：SignURL → 四元组
func GetJSAPIAuthConfig(ctx context.Context, url string) (*userv1.GetJSAPIAuthConfigResponse, error) {
	sig, err := feishu.SignURL(ctx, url)
	if err != nil {
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_UserError{
			UserError: &userv1.UserError{
				Code: userv1.UserErrorCode_USER_ERROR_CODE_INTERNAL_ERROR,
			},
		}, fmt.Sprintf("feishu sign url: %v", err))
	}
	return &userv1.GetJSAPIAuthConfigResponse{
		AppId:     sig.AppID,
		Timestamp: sig.Timestamp,
		NonceStr:  sig.NonceStr,
		Signature: sig.Signature,
	}, nil
}
