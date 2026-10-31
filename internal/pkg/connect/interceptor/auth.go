package interceptor

import (
	"context"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/errmsg"
	"github.com/rs/zerolog"
)

// connectRPC的一元认证拦截器，统一处理请求认证
// AuthUser is the minimal user representation used by the auth interceptor.
type AuthUser struct {
	UserID      int64  `json:"user_id"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	AccessToken string `json:"access_token,omitempty"`
}

// 抽象出会话存储，避免拦截器依赖具体数据库，让每个数据库自己实现
// SessionStore abstracts session lookup for the auth interceptor.
// Each microservice implements this with its own database/repository layer.
type SessionStore interface {
	GetSession(ctx context.Context, token string) (*AuthUser, error)
	GetUserByID(ctx context.Context, userID int64) (*AuthUser, error)
	SaveSession(ctx context.Context, token string, user *AuthUser) error
}

type contextKey string

const userContextKey contextKey = "auth_user"

// 从context取出用户并判断是否有效
// UserFromContext retrieves the authenticated user from context.
func UserFromContext(ctx context.Context) (*AuthUser, bool) {
	user, ok := ctx.Value(userContextKey).(*AuthUser)
	return user, ok && user != nil
}

// 把用户注入context
// SetUserToContext injects an authenticated user into context.
func SetUserToContext(ctx context.Context, user *AuthUser) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// 返回一个connect一元拦截器，进入handler前会先经过这里  一元 RPC：客户端发一个请求，服务端返回一个响应。
// AuthRequired returns a Connect interceptor that rejects unauthenticated requests.
// Set allowDevBypass to true in development to accept X-Dev-User-ID header.
func AuthRequired(store SessionStore, logger zerolog.Logger, allowDevBypass bool) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			// 从请求头 X-Dev-User-ID 读取用户 ID，模拟已登录用户。
			if allowDevBypass {
				if user, ok := devBypass(ctx, store, req); ok {
					return next(SetUserToContext(ctx, user), req)
				}
			}
			// 从 Authorization 请求头中提取 token。如果没有 token，直接返回未认证错误。
			token := extractToken(req)
			if token == "" {
				return nil, connect.NewError(errmsg.Unauthenticated.Code, errmsg.Unauthenticated)
			}

			user, err := store.GetSession(ctx, token)
			if err != nil {
				logger.Error().Err(err).Msg("session lookup failed")
				return nil, connect.NewError(errmsg.Unauthenticated.Code, errmsg.Unauthenticated)
			}
			if !isActiveUser(user) {
				return nil, connect.NewError(errmsg.Unauthenticated.Code, errmsg.Unauthenticated)
			}
			// next：下一个处理器，可能是另一个拦截器，也可能是最终业务 Handler。
			return next(SetUserToContext(ctx, user), req)

			/**
			  客户端请求
			     |
			     v
			  AuthRequired 拦截器
			     | 提取 token
			     | 查 session
			     | 校验用户是否 active
			     |
			     | 成功：SetUserToContext(ctx, user)
			     v
			  next(ctx, req)  -> 业务 Handler
			  **/
		}
	}
}

func devBypass(ctx context.Context, store SessionStore, req connect.AnyRequest) (*AuthUser, bool) {
	devUserID := req.Header().Get(constant.XDevUserIDHeader)
	if devUserID == "" {
		return nil, false
	}
	userID, err := strconv.ParseInt(devUserID, 10, 64)
	if err != nil || userID <= 0 {
		return nil, false
	}
	user, err := store.GetUserByID(ctx, userID)
	if err != nil || !isActiveUser(user) {
		return nil, false
	}
	return user, true
}

func isActiveUser(user *AuthUser) bool {
	return user != nil && user.UserID > 0 && strings.EqualFold(strings.TrimSpace(user.Status), "active")
}

func extractToken(req connect.AnyRequest) string {
	auth := req.Header().Get("Authorization")
	if auth == "" {
		return ""
	}
	if token, ok := strings.CutPrefix(auth, "Bearer "); ok {
		return token
	}
	return auth
}

// UserIDFromContext retrieves the authenticated user's ID from context.
func UserIDFromContext(ctx context.Context) (int64, error) {
	user, ok := UserFromContext(ctx)
	if !ok || user == nil || user.UserID <= 0 {
		return 0, connect.NewError(errmsg.Unauthenticated.Code, errmsg.Unauthenticated)
	}
	return user.UserID, nil
}
