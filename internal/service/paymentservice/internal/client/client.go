package client

import (
	"fmt"
	"net/http"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/errand/v1/errandv1connect"
	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/user/v1/userv1connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
)

var (
	UserInternalServiceClient       userv1connect.UserInternalServiceClient
	GroupTradeInternalServiceClient errandv1connect.GroupTradeInternalServiceClient
)

func InitUserServiceClient() {
	UserInternalServiceClient = userv1connect.NewUserInternalServiceClient(
		http.DefaultClient,
		fmt.Sprintf("%s:%d", config.AppConfig.UserServiceURL, config.AppConfig.UserServicePort),
	)
}

func InitGroupTradeInternalServiceClient() {
	GroupTradeInternalServiceClient = errandv1connect.NewGroupTradeInternalServiceClient(
		http.DefaultClient,
		fmt.Sprintf("%s:%d", config.AppConfig.ErrandServiceURL, config.AppConfig.ErrandServicePort),
	)
}
