package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/catalog/v1/catalogv1connect"
	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/service"
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
	stores, err := service.GetStoreList(ctx)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&catalogv1.GetStoreListResponse{
		Stores: stores,
	}), nil
}

func (s *CatalogServiceServer) CreateStore(
	ctx context.Context,
	r *connect.Request[catalogv1.CreateStoreRequest],
) (*connect.Response[catalogv1.CreateStoreResponse], error) {
	authUser, ok := interceptor.UserFromContext(ctx)
	if !ok {
		return nil, catalogError()
	}

	store, err := service.CreateStore(
		ctx,
		r.Msg.Name,
		r.Msg.Address,
		r.Msg.LogoUrl,
		r.Msg.ThemeColor,
		authUser.UserID,
	)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&catalogv1.CreateStoreResponse{
		Store: store,
	}), nil
}

func (s *CatalogServiceServer) UpdateStore(
	ctx context.Context,
	r *connect.Request[catalogv1.UpdateStoreRequest],
) (*connect.Response[catalogv1.UpdateStoreResponse], error) {
	store, err := service.UpdateStore(ctx, r.Msg.Store, r.Msg.UpdateMask.GetPaths())
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&catalogv1.UpdateStoreResponse{
		Store: store,
	}), nil
}

func InitCatalogServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := catalogv1connect.NewCatalogServiceHandler(&CatalogServiceServer{}, opts...)
	log.Debug().Msgf("CatalogService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
