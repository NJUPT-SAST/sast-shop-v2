package service

import (
	"context"

	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice/internal/repository"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice/internal/support"
	"github.com/uptrace/bun"
)

type pocketRoundKey struct{}

func normalRound(round int64) int64 {
	if round == 0 {
		return 1
	}
	return round
}

func requestedRound(ctx context.Context) int64 {
	round, ok := ctx.Value(pocketRoundKey{}).(int64)
	if !ok {
		return 1
	}
	return normalRound(round)
}

func checkPocketWrite(ctx context.Context, p *model.Pocket) error {
	if p.AdminOperationID != "" {
		return support.Failure(connect.CodeFailedPrecondition, "活动正在纠错，请稍后刷新")
	}
	if p.LifecycleRound != requestedRound(ctx) {
		return support.Failure(connect.CodeAborted, "活动已恢复至新轮次，请刷新后重试")
	}
	return nil
}

// LifecycleInterceptor checks the requested activity round before the RPC runs.
func (s *Service) LifecycleInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
			type roundMessage interface{ GetLifecycleRound() int64 }
			msg, ok := r.Any().(roundMessage)
			if !ok {
				return next(ctx, r)
			}
			expected := msg.GetLifecycleRound()
			if expected < 0 {
				return nil, ErrConflict
			}
			var pocketID int64
			if p, ok := r.Any().(interface{ GetPocketId() int64 }); ok {
				pocketID = p.GetPocketId()
			}
			if j, ok := r.Any().(interface{ GetJobId() int64 }); ok {
				job, e := repository.FindJob(ctx, s.DB, j.GetJobId())
				if e != nil {
					return nil, support.RPCError(e)
				}
				if job.PocketID == nil {
					return next(ctx, r)
				}
				pocketID = *job.PocketID
				if job.LifecycleRound != normalRound(expected) {
					return nil, ErrConflict
				}
			}
			if pocketID <= 0 {
				return next(ctx, r)
			}
			p, e := repository.FindPocket(ctx, s.DB, pocketID)
			if e != nil {
				return nil, support.RPCError(e)
			}
			ctx = context.WithValue(ctx, pocketRoundKey{}, normalRound(expected))
			if e := checkPocketWrite(ctx, p); e != nil {
				return nil, e
			}
			return next(ctx, r)
		}
	}
}

func lockAlbumPocket(ctx context.Context, tx bun.Tx, id int64) error {
	p, e := repository.LockPocketForShare(ctx, tx, id)
	if e != nil {
		return e
	}
	return checkPocketWrite(ctx, p)
}
