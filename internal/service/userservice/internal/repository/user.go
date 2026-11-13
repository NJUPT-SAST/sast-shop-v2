package repository

import (
	"context"
	"strings"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/model"
	"github.com/uptrace/bun"
)

func GetUserByID(ctx context.Context, userID int64) (*model.UserAccount, error) {
	var user model.UserAccount
	err := postgres.DB.NewSelect().Model(&user).Where("id = ?", userID).Scan(ctx)
	return &user, err
}

// SearchActiveUsers treats SQL wildcard characters as literal name characters.
func SearchActiveUsers(ctx context.Context, query string, afterID int64, limit int) ([]*model.UserAccount, error) {
	users := make([]*model.UserAccount, 0, limit)
	err := postgres.DB.NewSelect().Model(&users).
		Where("ua.status = ?", model.MemberStatusActive).
		Where("ua.id > ?", afterID).
		Where("ua.display_name ILIKE ? ESCAPE '!'", literalNamePattern(query)).
		OrderExpr("ua.id ASC").Limit(limit).Scan(ctx)
	return users, err
}

func literalNamePattern(query string) string {
	return "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(query) + "%"
}

func GetUsersByIDs(ctx context.Context, userIDs []int64) ([]*model.UserAccount, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	var users []*model.UserAccount
	err := postgres.DB.NewSelect().Model(&users).Where("id IN (?)", bun.List(userIDs)).Scan(ctx)
	return users, err
}
