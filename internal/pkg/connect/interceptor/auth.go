package interceptor

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
	"github.com/rs/zerolog"
)

// AuthUser is the minimal user representation used by the auth interceptor.
type AuthUser struct {
	UserID      int64  `json:"user_id"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	AccessToken string `json:"access_token,omitempty"`
}

// SessionStore abstracts session lookup for the auth interceptor.
// Each microservice implements this with its own database/repository layer.
type SessionStore interface {
	GetSession(ctx context.Context, token string) (*AuthUser, error)
	GetUserByID(ctx context.Context, userID int64) (*AuthUser, error)
	SaveSession(ctx context.Context, token string, user *AuthUser) error
}

type contextKey string

const userContextKey contextKey = "auth_user"

// UserFromContext retrieves the authenticated user from context.
func UserFromContext(ctx context.Context) (*AuthUser, bool) {
	user, ok := ctx.Value(userContextKey).(*AuthUser)
	return user, ok
}

// SetUserToContext injects an authenticated user into context.
func SetUserToContext(ctx context.Context, user *AuthUser) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// AuthRequired returns a Connect interceptor that rejects unauthenticated requests.
// Set allowDevBypass to true in development to accept X-Dev-User-ID header.
func AuthRequired(store SessionStore, logger zerolog.Logger, allowDevBypass bool) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if allowDevBypass {
				if user, ok := devBypass(ctx, store, req); ok {
					return next(SetUserToContext(ctx, user), req)
				}
			}

			token := extractToken(req)
			if token == "" {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("missing authorization header"))
			}

			user, err := store.GetSession(ctx, token)
			if err != nil {
				logger.Error().Err(err).Msg("session lookup failed")
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid or expired session"))
			}

			return next(SetUserToContext(ctx, user), req)
		}
	}
}

func devBypass(ctx context.Context, store SessionStore, req connect.AnyRequest) (*AuthUser, bool) {
	devUserID := req.Header().Get(constant.XDevUserIDHeader)
	if devUserID == "" {
		return nil, false
	}
	userID, err := strconv.ParseInt(devUserID, 10, 64)
	if err != nil {
		return nil, false
	}
	user, err := store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, false
	}
	return user, true
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
