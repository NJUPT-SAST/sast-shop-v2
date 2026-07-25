package v1

import (
	"context"
	"errors"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/errand/v1/errandv1connect"
	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	rpcinterceptor "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/service"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type BuyerErrandOrderServiceServer struct {
	errandv1connect.BuyerErrandOrderServiceHandler
}

func authenticatedBuyerUserID(ctx context.Context) (int64, error) {
	user, ok := rpcinterceptor.UserFromContext(ctx)
	if !ok || user == nil || user.UserID <= 0 {
		return 0, connect.NewError(connect.CodeUnauthenticated, errors.New("missing authenticated user"))
	}
	return user.UserID, nil
}

func (s *BuyerErrandOrderServiceServer) GetBuyerErrandOrderBrief(
	ctx context.Context,
	r *connect.Request[errandv1.GetBuyerErrandOrderBriefRequest],
) (*connect.Response[errandv1.GetBuyerErrandOrderBriefResponse], error) {
	return nil, errandError()
}

func (s *BuyerErrandOrderServiceServer) GetBuyerErrandOrderDetail(
	ctx context.Context,
	r *connect.Request[errandv1.GetBuyerErrandOrderDetailRequest],
) (*connect.Response[errandv1.GetBuyerErrandOrderDetailResponse], error) {
	return nil, errandError()
}

func (s *BuyerErrandOrderServiceServer) GetBuyerErrandOrderCaptainContact(
	ctx context.Context,
	r *connect.Request[errandv1.GetBuyerErrandOrderCaptainContactRequest],
) (*connect.Response[errandv1.GetBuyerErrandOrderCaptainContactResponse], error) {
	userID, err := authenticatedBuyerUserID(ctx)
	if err != nil {
		return nil, err
	}

	openID, err := service.GetBuyerErrandOrderCaptainContact(ctx, userID, r.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&errandv1.GetBuyerErrandOrderCaptainContactResponse{
		CaptainFeishuOpenId: openID,
	}), nil
}

func InitBuyerErrandOrderServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := errandv1connect.NewBuyerErrandOrderServiceHandler(&BuyerErrandOrderServiceServer{}, opts...)
	log.Debug().Msgf("BuyerErrandOrderService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
