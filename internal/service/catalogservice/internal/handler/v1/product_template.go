package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/catalog/v1/catalogv1connect"
	catalogv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/catalog/v1"
	"connectrpc.com/connect"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type ProductTemplateServiceServer struct {
	catalogv1connect.ProductTemplateServiceHandler
}

func (s *ProductTemplateServiceServer) GetProductTemplateList(
	ctx context.Context,
	r *connect.Request[catalogv1.GetProductTemplateListRequest],
) (*connect.Response[catalogv1.GetProductTemplateListResponse], error) {
	return nil, catalogError()
}

func (s *ProductTemplateServiceServer) CreateProductTemplate(
	ctx context.Context,
	r *connect.Request[catalogv1.CreateProductTemplateRequest],
) (*connect.Response[catalogv1.CreateProductTemplateResponse], error) {
	return nil, catalogError()
}

func (s *ProductTemplateServiceServer) UpdateProductTemplate(
	ctx context.Context,
	r *connect.Request[catalogv1.UpdateProductTemplateRequest],
) (*connect.Response[catalogv1.UpdateProductTemplateResponse], error) {
	return nil, catalogError()
}

func (s *ProductTemplateServiceServer) GetProductTemplateByBarcode(
	ctx context.Context,
	r *connect.Request[catalogv1.GetProductTemplateByBarcodeRequest],
) (*connect.Response[catalogv1.GetProductTemplateByBarcodeResponse], error) {
	return nil, catalogError()
}

func InitProductTemplateServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := catalogv1connect.NewProductTemplateServiceHandler(&ProductTemplateServiceServer{}, opts...)
	log.Debug().Msgf("ProductTemplateService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
