package client

import (
	"context"
	"fmt"
	"net/http"

	userv1connect "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/user/v1/userv1connect"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
)

var UserInternalClient userv1connect.UserInternalServiceClient

func InitUserClient() {
	UserInternalClient = userv1connect.NewUserInternalServiceClient(
		http.DefaultClient,
		fmt.Sprintf("%s:%d", config.AppConfig.UserServiceURL, config.AppConfig.UserServicePort),
	)
}

func GetUsers(ctx context.Context, ids []int64) ([]*userv1.UserInfo, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	resp, err := UserInternalClient.GetUsers(ctx, connect.NewRequest(&userv1.GetUsersRequest{
		UserIds: ids,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.Users, nil
}
