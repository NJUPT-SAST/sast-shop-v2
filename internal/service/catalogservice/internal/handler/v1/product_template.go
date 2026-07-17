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

type ProductTemplateServiceServer struct {
	catalogv1connect.ProductTemplateServiceHandler
}

func (s *ProductTemplateServiceServer) GetProductTemplateList(
	ctx context.Context,
	r *connect.Request[catalogv1.GetProductTemplateListRequest],
) (*connect.Response[catalogv1.GetProductTemplateListResponse], error) {
	pts, total, err := service.GetProductTemplateList(ctx, r.Msg.StoreId, r.Msg.Page, r.Msg.PageSize)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&catalogv1.GetProductTemplateListResponse{
		ProductTemplates: pts,
		CurrentPage:      r.Msg.Page,
		TotalCount:       total,
	}), nil
}

func (s *ProductTemplateServiceServer) CreateProductTemplate(
	ctx context.Context,
	r *connect.Request[catalogv1.CreateProductTemplateRequest],
) (*connect.Response[catalogv1.CreateProductTemplateResponse], error) {
	authUser, ok := interceptor.UserFromContext(ctx)
	if !ok {
		return nil, catalogError()
	}

	pt, err := service.CreateProductTemplate(
		ctx,
		r.Msg.StoreId,
		r.Msg.Title,
		r.Msg.Description,
		r.Msg.PriceCents,
		r.Msg.MainImageUrl,
		r.Msg.Barcode,
		authUser.UserID,
	)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&catalogv1.CreateProductTemplateResponse{
		ProductTemplate: pt,
	}), nil
}

func (s *ProductTemplateServiceServer) UpdateProductTemplate(
	ctx context.Context,
	r *connect.Request[catalogv1.UpdateProductTemplateRequest],
) (*connect.Response[catalogv1.UpdateProductTemplateResponse], error) {
	pt, err := service.UpdateProductTemplate(ctx, r.Msg.ProductTemplate, r.Msg.UpdateMask.GetPaths())
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&catalogv1.UpdateProductTemplateResponse{
		ProductTemplate: pt,
	}), nil
}

func (s *ProductTemplateServiceServer) GetProductTemplateByBarcode(
	ctx context.Context,
	r *connect.Request[catalogv1.GetProductTemplateByBarcodeRequest],
) (*connect.Response[catalogv1.GetProductTemplateByBarcodeResponse], error) {
	items, err := service.GetProductTemplateByBarcode(ctx, r.Msg.Barcode)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&catalogv1.GetProductTemplateByBarcodeResponse{
		Items: items,
	}), nil
}

func InitProductTemplateServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := catalogv1connect.NewProductTemplateServiceHandler(&ProductTemplateServiceServer{}, opts...)
	log.Debug().Msgf("ProductTemplateService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
