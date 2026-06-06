package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/catalog/v1/catalogv1connect"
	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	"connectrpc.com/connect"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type CatalogServiceServer struct {
	catalogv1connect.CatalogServiceHandler
}

func (s *CatalogServiceServer) GetStoreList(
	ctx context.Context,
	r *connect.Request[catalogv1.GetStoreListRequest],
) (*connect.Response[catalogv1.GetStoreListResponse], error) {
	return nil, catalogError()
}

func (s *CatalogServiceServer) CreateStore(
	ctx context.Context,
	r *connect.Request[catalogv1.CreateStoreRequest],
) (*connect.Response[catalogv1.CreateStoreResponse], error) {
	return nil, catalogError()
}

func (s *CatalogServiceServer) UpdateStore(
	ctx context.Context,
	r *connect.Request[catalogv1.UpdateStoreRequest],
) (*connect.Response[catalogv1.UpdateStoreResponse], error) {
	return nil, catalogError()
}

func InitCatalogServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := catalogv1connect.NewCatalogServiceHandler(&CatalogServiceServer{}, opts...)
	log.Debug().Msgf("CatalogService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
