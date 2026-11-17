package admin

import (
	"context"
	"database/sql"
	"errors"

	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/uptrace/bun"
)

const IdentityLock int64 = 74619382051

func Require(ctx context.Context) (*interceptor.AuthUser, error) {
	user, ok := interceptor.UserFromContext(ctx)
	if !ok || user.UserID <= 0 || user.Status != "active" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("请重新登录"))
	}
	if user.Role != "admin" && user.Role != "superadmin" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("需要管理员权限"))
	}
	return user, nil
}

func RequireSuper(ctx context.Context) (*interceptor.AuthUser, error) {
	user, err := Require(ctx)
	if err != nil {
		return nil, err
	}
	if user.Role != "superadmin" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("需要超级管理员权限"))
	}
	return user, nil
}

// RequireCurrent holds the identity row through the caller's write transaction.
func RequireCurrent(ctx context.Context, db bun.IDB, super bool) (*interceptor.AuthUser, error) {
	user, err := Require(ctx)
	if err != nil {
		return nil, err
	}
	var current struct {
		ID          int64  `bun:"id"`
		Role        string `bun:"role"`
		Status      string `bun:"status"`
		AuthVersion int64  `bun:"auth_version"`
	}
	err = db.NewSelect().TableExpr(`"user".user_account`).
		Column("id", "role", "status", "auth_version").Where("id = ?", user.UserID).
		For("SHARE").Scan(ctx, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("账号不存在"))
	}
	if err != nil {
		return nil, err
	}
	version := user.AuthVersion
	if version == 0 {
		version = 1
	}
	if current.Status != "active" || current.AuthVersion != version || current.Role != user.Role {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("账号权限已变更，请重新登录"))
	}
	if current.Role != "admin" && current.Role != "superadmin" || super && current.Role != "superadmin" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("没有执行该操作的权限"))
	}
	return user, nil
}
