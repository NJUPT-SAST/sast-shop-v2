package interceptor

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/emptypb"
)

type authTestStore struct{ user *AuthUser }

func (s authTestStore) GetSession(context.Context, string) (*AuthUser, error) { return s.user, nil }
func (s authTestStore) GetUserByID(context.Context, int64) (*AuthUser, error) { return s.user, nil }
func (s authTestStore) SaveSession(context.Context, string, *AuthUser) error  { return nil }

func TestAuthRequiredRejectsInvalidUsers(t *testing.T) {
	for _, user := range []*AuthUser{
		nil,
		{UserID: 0, Status: "active"},
		{UserID: -1, Status: "active"},
		{UserID: 1, Status: ""},
		{UserID: 1, Status: "restricted"},
		{UserID: 1, Status: "banned"},
		{UserID: 1, Status: "deleted"},
	} {
		for _, devBypass := range []bool{false, true} {
			called := false
			handler := AuthRequired(
				authTestStore{user},
				zerolog.Nop(),
				devBypass,
			)(
				func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
					called = true
					return connect.NewResponse(&emptypb.Empty{}), nil
				},
			)
			req := connect.NewRequest(&emptypb.Empty{})
			if devBypass {
				req.Header().Set("X-Dev-User-ID", "1")
			} else {
				req.Header().Set("Authorization", "Bearer token")
			}
			_, err := handler(context.Background(), req)
			if called || connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Errorf("user %#v, bypass %v: called=%v err=%v", user, devBypass, called, err)
			}
		}
	}
}

func TestAuthRequiredAcceptsActiveUser(t *testing.T) {
	user := &AuthUser{UserID: 1, Status: "active"}
	handler := AuthRequired(
		authTestStore{user},
		zerolog.Nop(),
		false,
	)(
		func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
			if got, ok := UserFromContext(ctx); !ok || got != user {
				t.Fatalf("authenticated user missing from context: %#v", got)
			}
			return connect.NewResponse(&emptypb.Empty{}), nil
		},
	)
	req := connect.NewRequest(&emptypb.Empty{})
	req.Header().Set("Authorization", "Bearer token")
	if _, err := handler(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}
