package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/catalog/v1/catalogv1connect"
	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	"connectrpc.com/connect"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type CatalogInternalServer struct {
	catalogv1connect.CatalogInternalServiceHandler
}

func (s *CatalogInternalServer) GetProductTemplate(
	ctx context.Context,
	r *connect.Request[catalogv1.GetProductTemplateRequest],
) (*connect.Response[catalogv1.GetProductTemplateResponse], error) {
	return nil, catalogError()
}

func (s *CatalogInternalServer) GetStore(
	ctx context.Context,
	r *connect.Request[catalogv1.GetStoreRequest],
) (*connect.Response[catalogv1.GetStoreResponse], error) {
	return nil, catalogError()
}

func InitCatalogInternalServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := catalogv1connect.NewCatalogInternalServiceHandler(&CatalogInternalServer{}, opts...)
	log.Debug().Msgf("CatalogInternalService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
