package service

import (
	"crypto/rand"
	"encoding/hex"

	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	rpcinterceptor "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/model"
)

//生成后端自有 access_token，32 字节随机 hex
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

//构造存入 Redis 的 AuthUser，AccessToken 存飞书 user_access_token
func buildAuthUser(u *model.UserAccount, feishuAccessToken string) *rpcinterceptor.AuthUser {
	return &rpcinterceptor.AuthUser{
		UserID:      u.ID,
		Role:        string(u.Role),
		Status:      string(u.Status),
		AccessToken: feishuAccessToken,
	}
}

//UserAccount → proto LoginMember 映射
func toLoginMember(u *model.UserAccount) *userv1.LoginMember {
	return &userv1.LoginMember{
		Id:          u.ID,
		DisplayName: u.DisplayName,
		AvatarUrl:   u.AvatarURL,
		Role:        string(u.Role),
		Status:      string(u.Status),
	}
}
