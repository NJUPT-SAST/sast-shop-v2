package client

import (
	"fmt"
	"net/http"
	"time"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/catalog/v1/catalogv1connect"
	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/payment/v1/paymentv1connect"
	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/user/v1/userv1connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/discovery"
	"github.com/hashicorp/consul/api"
)

var (
	CatalogInternalServiceClient catalogv1connect.CatalogInternalServiceClient
	PaymentInternalServiceClient paymentv1connect.PaymentInternalServiceClient
	UserInternalServiceClient    userv1connect.UserInternalServiceClient
)

func Init(consulClient *api.Client, catalogName string) {
	// 获取http传输层的独立副本，以便对其进行自定义修改，不会影响全局的默认配置
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil

	rpcHTTPClient := &http.Client{
		Transport: &discovery.Transport{
			ConsulClient: consulClient,
			ServiceName:  catalogName,
			Base:         transport,
		},
		Timeout: 5 * time.Second,
	}
	CatalogInternalServiceClient = catalogv1connect.NewCatalogInternalServiceClient(
		rpcHTTPClient,
		"http://"+catalogName,
	)
	PaymentInternalServiceClient = paymentv1connect.NewPaymentInternalServiceClient(
		rpcHTTPClient,
		fmt.Sprintf("%s:%d", config.AppConfig.PaymentServiceURL, config.AppConfig.PaymentServicePort),
	)
	UserInternalServiceClient = userv1connect.NewUserInternalServiceClient(
		rpcHTTPClient,
		fmt.Sprintf("%s:%d", config.AppConfig.UserServiceURL, config.AppConfig.UserServicePort),
	)
}
