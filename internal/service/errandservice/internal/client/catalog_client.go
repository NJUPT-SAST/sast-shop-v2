package client

import (
	"context"
	"fmt"
	"net/http"

	catalogv1connect "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/catalog/v1/catalogv1connect"
	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
)

var CatalogInternalClient catalogv1connect.CatalogInternalServiceClient

func InitCatalogClient() {
	CatalogInternalClient = catalogv1connect.NewCatalogInternalServiceClient(
		http.DefaultClient,
		fmt.Sprintf("%s:%d", config.AppConfig.CatalogServiceURL, config.AppConfig.CatalogServicePort),
	)
}

func GetProductTemplate(ctx context.Context, id int64) (*catalogv1.ProductTemplate, error) {
	resp, err := CatalogInternalClient.GetProductTemplate(ctx, connect.NewRequest(&catalogv1.GetProductTemplateRequest{
		ProductTemplateId: id,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.ProductTemplate, nil
}

func GetStore(ctx context.Context, id int64) (*catalogv1.Store, error) {
	resp, err := CatalogInternalClient.GetStore(ctx, connect.NewRequest(&catalogv1.GetStoreRequest{
		StoreId: id,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.Store, nil
}
