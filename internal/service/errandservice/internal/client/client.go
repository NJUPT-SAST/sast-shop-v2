package client

import (
	"fmt"
	"net/http"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/payment/v1/paymentv1connect"
	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/user/v1/userv1connect" 
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
)

var PaymentInternalServiceClient paymentv1connect.PaymentInternalServiceClient
var UserInternalServiceClient userv1connect.UserInternalServiceClient
func Init() {
	PaymentInternalServiceClient = paymentv1connect.NewPaymentInternalServiceClient(
		http.DefaultClient,
		fmt.Sprintf("%s:%d", config.AppConfig.PaymentServiceURL, config.AppConfig.PaymentServicePort),
	)

	UserInternalServiceClient = userv1connect.NewUserInternalServiceClient(
		http.DefaultClient,
		fmt.Sprintf("%s:%d", config.AppConfig.UserServiceURL, config.AppConfig.UserServicePort),
	)



	
}
