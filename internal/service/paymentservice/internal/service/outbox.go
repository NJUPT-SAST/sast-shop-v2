package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	ev "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/client"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/repository"
)

func deliverPaymentConfirmation(ctx context.Context, bill *model.PaymentBill) error {
	if bill.SourceType == nil || bill.SourceID == nil || *bill.SourceType != "errand_task" {
		return nil
	}
	if client.GroupTradeInternalServiceClient == nil {
		return fmt.Errorf("payment callback client is unavailable")
	}
	if _, err := client.GroupTradeInternalServiceClient.OnPaymentConfirmed(
		ctx,
		connect.NewRequest(
			&ev.OnPaymentConfirmedRequest{
				SourceType:     *bill.SourceType,
				SourceId:       *bill.SourceID,
				PayerId:        bill.PayerID,
				LifecycleRound: bill.LifecycleRound,
			},
		),
	); err != nil {
		return err
	}
	count, err := repository.CountIncompleteBillsBySource(ctx, *bill.SourceType, *bill.SourceID)
	if err != nil {
		return err
	}
	if count == 0 {
		_, err = client.GroupTradeInternalServiceClient.OnAllPaymentsConfirmed(
			ctx,
			connect.NewRequest(
				&ev.OnAllPaymentsConfirmedRequest{
					SourceType:     *bill.SourceType,
					SourceId:       *bill.SourceID,
					LifecycleRound: bill.LifecycleRound,
				},
			),
		)
	}
	return err
}

func RunPaymentOutbox(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			processPaymentOutbox(ctx)
		}
	}
}

func processPaymentOutbox(ctx context.Context) {
	var events []struct{ ID, BillID, LifecycleRound int64 }
	err := postgres.DB.NewRaw(`WITH ready AS (
 SELECT id FROM payment.business_outbox WHERE status IN ('pending','processing') AND next_attempt_at<=now() ORDER BY id LIMIT 5 FOR UPDATE SKIP LOCKED
 ) UPDATE payment.business_outbox o SET status='processing',attempts=attempts+1,next_attempt_at=now()+interval '90 seconds',updated_at=now()
 FROM ready WHERE o.id=ready.id RETURNING o.id,o.bill_id,o.lifecycle_round`).Scan(ctx, &events)
	if err != nil {
		return
	}
	for _, event := range events {
		eventCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		bill, err := repository.GetBillByID(eventCtx, event.BillID)
		state := "delivered"
		if err == nil &&
			(bill.SupersededAt != nil || bill.LifecycleRound != event.LifecycleRound || bill.Status != model.PaymentBillStatusCompleted) {
			state = "superseded"
		} else if err == nil {
			err = deliverPaymentConfirmation(eventCtx, bill)
		}
		cancel()
		message := ""
		if err != nil {
			state = "pending"
			message = "等待关联业务确认"
		}
		_, saveErr := postgres.DB.ExecContext(
			ctx,
			`UPDATE payment.business_outbox SET status=?,last_error=?,next_attempt_at=now()+interval '30 seconds',updated_at=now()WHERE id=?`,
			state,
			message,
			event.ID,
		)
		if saveErr != nil {
			log.Error().Err(saveErr).Int64("event_id", event.ID).Msg("persist payment callback state")
		}
	}
}
