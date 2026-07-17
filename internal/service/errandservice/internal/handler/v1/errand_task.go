package v1

import (
	"context"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/errand/v1/errandv1connect"
	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/service"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

type ErrandTaskServiceServer struct {
	errandv1connect.ErrandTaskServiceHandler
}

func (s *ErrandTaskServiceServer) CreateTask(
	ctx context.Context,
	r *connect.Request[errandv1.CreateTaskRequest],
) (*connect.Response[errandv1.CreateTaskResponse], error) {
	captainID, err := captainIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	taskID, err := service.CreateTask(ctx, captainID, r.Msg)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&errandv1.CreateTaskResponse{
		ErrandTaskId: taskID,
	}), nil
}

func (s *ErrandTaskServiceServer) GetShoppingTaskDetail(
	ctx context.Context,
	r *connect.Request[errandv1.GetShoppingTaskDetailRequest],
) (*connect.Response[errandv1.GetShoppingTaskDetailResponse], error) {
	captainID, err := captainIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := service.GetShoppingTaskDetail(ctx, captainID, r.Msg)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(resp), nil
}

func (s *ErrandTaskServiceServer) SaveShoppingTaskItem(
	ctx context.Context,
	r *connect.Request[errandv1.SaveShoppingTaskItemRequest],
) (*connect.Response[errandv1.SaveShoppingTaskItemResponse], error) {
	captainID, err := captainIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := service.SaveShoppingTaskItem(ctx, captainID, r.Msg); err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&errandv1.SaveShoppingTaskItemResponse{}), nil
}

func (s *ErrandTaskServiceServer) TransitionToPendingDistributing(
	ctx context.Context,
	r *connect.Request[errandv1.TransitionToPendingDistributingRequest],
) (*connect.Response[errandv1.TransitionToPendingDistributingResponse], error) {
	captainID, err := captainIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := service.TransitionToPendingDistributing(ctx, captainID, r.Msg); err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&errandv1.TransitionToPendingDistributingResponse{}), nil
}

func (s *ErrandTaskServiceServer) GetDistributingTaskDetail(
	ctx context.Context,
	r *connect.Request[errandv1.GetDistributingTaskDetailRequest],
) (*connect.Response[errandv1.GetDistributingTaskDetailResponse], error) {
	captainID, err := captainIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := service.GetDistributingTaskDetail(ctx, captainID, r.Msg)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(resp), nil
}

func (s *ErrandTaskServiceServer) UpdateActualPrice(
	ctx context.Context,
	r *connect.Request[errandv1.UpdateActualPriceRequest],
) (*connect.Response[errandv1.UpdateActualPriceResponse], error) {
	captainID, err := captainIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := service.UpdateActualPrice(ctx, captainID, r.Msg); err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&errandv1.UpdateActualPriceResponse{}), nil
}

func (s *ErrandTaskServiceServer) TransitionToDistributing(
	ctx context.Context,
	r *connect.Request[errandv1.TransitionToDistributingRequest],
) (*connect.Response[errandv1.TransitionToDistributingResponse], error) {
	captainID, err := captainIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := service.TransitionToDistributing(ctx, captainID, r.Msg); err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&errandv1.TransitionToDistributingResponse{}), nil
}

func (s *ErrandTaskServiceServer) SaveDistributingTaskAssignment(
	ctx context.Context,
	r *connect.Request[errandv1.SaveDistributingTaskAssignmentRequest],
) (*connect.Response[errandv1.SaveDistributingTaskAssignmentResponse], error) {
	captainID, err := captainIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := service.SaveDistributingTaskAssignment(ctx, captainID, r.Msg); err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&errandv1.SaveDistributingTaskAssignmentResponse{}), nil
}

func (s *ErrandTaskServiceServer) TransitionToCollectingPayment(
	ctx context.Context,
	r *connect.Request[errandv1.TransitionToCollectingPaymentRequest],
) (*connect.Response[errandv1.TransitionToCollectingPaymentResponse], error) {
	captainID, err := captainIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := service.TransitionToCollectingPayment(ctx, captainID, r.Msg); err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&errandv1.TransitionToCollectingPaymentResponse{}), nil
}

func (s *ErrandTaskServiceServer) GetCollectingPaymentDetail(
	ctx context.Context,
	r *connect.Request[errandv1.GetCollectingPaymentDetailRequest],
) (*connect.Response[errandv1.GetCollectingPaymentDetailResponse], error) {
	captainID, err := captainIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := service.GetCollectingPaymentDetail(ctx, captainID, r.Msg)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(resp), nil
}

func (s *ErrandTaskServiceServer) TransitionToCompleted(
	ctx context.Context,
	r *connect.Request[errandv1.TransitionToCompletedRequest],
) (*connect.Response[errandv1.TransitionToCompletedResponse], error) {
	captainID, err := captainIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := service.TransitionToCompleted(ctx, captainID, r.Msg); err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&errandv1.TransitionToCompletedResponse{}), nil
}

func (s *ErrandTaskServiceServer) GetErrandTaskList(
	ctx context.Context,
	r *connect.Request[errandv1.GetErrandTaskListRequest],
) (*connect.Response[errandv1.GetErrandTaskListResponse], error) {
	captainID, err := captainIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := service.GetErrandTaskList(ctx, captainID, r.Msg)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(resp), nil
}

func (s *ErrandTaskServiceServer) CancelTask(
	ctx context.Context,
	r *connect.Request[errandv1.CancelTaskRequest],
) (*connect.Response[errandv1.CancelTaskResponse], error) {
	captainID, err := captainIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := service.CancelTask(ctx, captainID, r.Msg); err != nil {
		return nil, mapServiceError(err)
	}
	return connect.NewResponse(&errandv1.CancelTaskResponse{}), nil
}

func InitErrandTaskServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := errandv1connect.NewErrandTaskServiceHandler(&ErrandTaskServiceServer{}, opts...)
	log.Debug().Msgf("ErrandTaskService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
