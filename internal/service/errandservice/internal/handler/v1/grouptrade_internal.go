package v1

import (
	"context"
	"errors"

	"buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/errand/v1/errandv1connect"
	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/service"
	"github.com/labstack/echo/v5"
	"github.com/rs/zerolog/log"
)

// 跑腿账单约定：source_type = "errand_task"、source_id = errand_task.id
const SourceTypeErrandTask = "errand_task"

type GroupTradeInternalServer struct {
	errandv1connect.GroupTradeInternalServiceHandler
}

// 确认单笔账单完成
func (s *GroupTradeInternalServer) OnPaymentConfirmed(
	ctx context.Context,
	r *connect.Request[errandv1.OnPaymentConfirmedRequest],
) (*connect.Response[errandv1.OnPaymentConfirmedResponse], error) {
	msg := r.Msg
	log.Debug().
		Str("source_type", msg.SourceType).
		Int64("source_id", msg.SourceId).
		Int64("payer_id", msg.PayerId).
		Msg("OnPaymentConfirmed called")

	// 入参校验(大于0)
	if msg.SourceId <= 0 || msg.PayerId <= 0 {
		log.Warn().Msg("OnPaymentConfirmed: invalid source_id or payer_id")
		return nil, errandError()
	}

	// source_type 路由
	switch msg.SourceType {
	case SourceTypeErrandTask:
		err := service.OnErrandTaskPaymentConfirmed(ctx, msg.SourceId, msg.PayerId)
		if err != nil {
			// service 层已经记过日志了，这里只把它翻译成 connect.Error
			if errors.Is(err, service.ErrAssignmentNotFound) {
				log.Warn().
					Int64("task_id", msg.SourceId).
					Int64("payer_id", msg.PayerId).
					Msg("assignment not found for payment")
			}
			return nil, errandError()
		}
		return connect.NewResponse(&errandv1.OnPaymentConfirmedResponse{}), nil

	default:
		log.Warn().Str("source_type", msg.SourceType).Msg("unsupported source_type")
		return nil, errandError()
	}
}

// 所有账单完成，把 errand_task 推到 completed 。
func (s *GroupTradeInternalServer) OnAllPaymentsConfirmed(
	ctx context.Context,
	r *connect.Request[errandv1.OnAllPaymentsConfirmedRequest],
) (*connect.Response[errandv1.OnAllPaymentsConfirmedResponse], error) {
	msg := r.Msg
	log.Debug().
		Str("source_type", msg.SourceType).
		Int64("source_id", msg.SourceId).
		Msg("OnAllPaymentsConfirmed called")

	if msg.SourceId <= 0 {
		log.Warn().Msg("OnAllPaymentsConfirmed: invalid source_id")
		return nil, errandError()
	}

	switch msg.SourceType {
	case SourceTypeErrandTask:
		if err := service.OnErrandTaskAllPaymentsConfirmed(ctx, msg.SourceId); err != nil {
			return nil, errandError()
		}
		return connect.NewResponse(&errandv1.OnAllPaymentsConfirmedResponse{}), nil

	default:
		log.Warn().Str("source_type", msg.SourceType).Msg("unsupported source_type")
		return nil, errandError()
	}
}

func InitGroupTradeInternalServiceHandler(e *echo.Echo, opts ...connect.HandlerOption) {
	apiPath, apiHandler := errandv1connect.NewGroupTradeInternalServiceHandler(&GroupTradeInternalServer{}, opts...)
	log.Debug().Msgf("GroupTradeInternalService API registered at path: %s", apiPath)
	e.Any(apiPath+"*", echo.WrapHandler(apiHandler))
}
