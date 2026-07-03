package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/model"
)

func GetUserByFeishuOpenID(ctx context.Context, openID string) (*model.UserAccount, error) {
	var user model.UserAccount
	err := postgres.DB.NewSelect().
		Model(&user).
		Where("feishu_open_id = ?", openID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func UpsertUser(ctx context.Context, openID, displayName, avatarURL string) (*model.UserAccount, error) {
	existing, err := GetUserByFeishuOpenID(ctx, openID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if existing != nil {
		existing.DisplayName = displayName
		existing.AvatarURL = avatarURL
		existing.LastLoginAt = now
		existing.UpdatedAt = now
		_, err = postgres.DB.NewUpdate().
			Model(existing).
			Column("display_name", "avatar_url", "last_login_at", "updated_at").
			WherePK().
			Exec(ctx)
		if err != nil {
			return nil, err
		}
		return existing, nil
	}

	user := &model.UserAccount{
		FeishuOpenID: openID,
		DisplayName:  displayName,
		AvatarURL:    avatarURL,
		Role:         model.MemberRoleUser,
		Status:       model.MemberStatusActive,
		LastLoginAt:  now,
	}
	_, err = postgres.DB.NewInsert().Model(user).Returning("*").Exec(ctx)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func CreateAuthSession(
	ctx context.Context,
	userID int64,
	refreshTokenHash string,
	expiresAt time.Time,
) (*model.AuthSession, error) {
	session := &model.AuthSession{
		UserID:           userID,
		RefreshTokenHash: refreshTokenHash,
		Status:           model.AuthSessionStatusActive,
		ExpiresAt:        expiresAt,
	}
	_, err := postgres.DB.NewInsert().Model(session).Returning("*").Exec(ctx)
	if err != nil {
		return nil, err
	}
	return session, nil
}
