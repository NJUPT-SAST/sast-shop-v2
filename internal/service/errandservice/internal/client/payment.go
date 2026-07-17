package client

import (
	"fmt"
	"net/http"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/payment/v1/paymentv1connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
)

var PaymentInternalServiceClient paymentv1connect.PaymentInternalServiceClient

func InitPaymentServiceClient() {
	PaymentInternalServiceClient = paymentv1connect.NewPaymentInternalServiceClient(
		http.DefaultClient,
		fmt.Sprintf("%s:%d", config.AppConfig.PaymentServiceURL, config.AppConfig.PaymentServicePort),
	)
}
