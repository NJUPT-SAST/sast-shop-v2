package repository

import (
	"context"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/model"
	"github.com/uptrace/bun"
)

func GetUserByID(ctx context.Context, userID int64) (*model.UserAccount, error) {
	var user model.UserAccount
	err := postgres.DB.NewSelect().Model(&user).Where("id = ?", userID).Scan(ctx)
	return &user, err
}

func GetUsersByIDs(ctx context.Context, userIDs []int64) ([]*model.UserAccount, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	var users []*model.UserAccount
	err := postgres.DB.NewSelect().Model(&users).Where("id IN (?)", bun.List(userIDs)).Scan(ctx)
	return users, err
}
