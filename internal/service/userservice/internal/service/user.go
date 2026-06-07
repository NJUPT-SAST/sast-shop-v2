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

func GetByUserIDs(ctx context.Context, userIDs []int64) ([]*model.UserAccount, error) {
	return repository.GetUsersByIDs(ctx, userIDs)
}
