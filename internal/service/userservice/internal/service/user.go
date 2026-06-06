package service

import (
	"context"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/repository"
)

func GetUserInfo(ctx context.Context, userID int64) (*model.UserAccount, error) {
	return repository.GetUserByID(ctx, userID)
}

func GetByUserIDs(ctx context.Context, userIDs []int64) ([]*model.UserAccount, error) {
	return repository.GetUsersByIDs(ctx, userIDs)
}
