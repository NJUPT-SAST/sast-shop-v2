package service

import (
	"context"

	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/repository"
	"github.com/rs/zerolog/log"
)

// GetUserInfo returns a single user by ID (used by public-facing UserService).
func GetUserInfo(ctx context.Context, userID int64) (*model.UserAccount, error) {
	user, err := repository.GetUserByID(ctx, userID)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get user info for userID: %d", userID)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_UserError{
			UserError: &userv1.UserError{
				Code: userv1.UserErrorCode_USER_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}
	return user, nil
}

// GetInternalUsers returns users by IDs for internal service-to-service calls.
// Returns InternalUserInfo (with feishu_open_id) rather than public UserInfo.
func GetInternalUsers(ctx context.Context, userIDs []int64) ([]*userv1.InternalUserInfo, error) {
	users, err := repository.GetUsersByIDs(ctx, userIDs)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get users for userIDs: %v", userIDs)
		return nil, rpcerror.NewInternalError(&commonv1.BusinessError_UserError{
			UserError: &userv1.UserError{
				Code: userv1.UserErrorCode_USER_ERROR_CODE_INTERNAL_ERROR,
			},
		}, "")
	}

	result := make([]*userv1.InternalUserInfo, len(users))
	for i, u := range users {
		result[i] = userAccountToInternalUserInfo(u)
	}
	return result, nil
}

// userAccountToInternalUserInfo converts a DB model to an InternalUserInfo proto.
func userAccountToInternalUserInfo(u *model.UserAccount) *userv1.InternalUserInfo {
	return &userv1.InternalUserInfo{
		Id:           u.ID,
		Name:         u.DisplayName,
		AvatarUrl:    u.AvatarURL,
		FeishuOpenId: u.FeishuOpenID,
	}
}
