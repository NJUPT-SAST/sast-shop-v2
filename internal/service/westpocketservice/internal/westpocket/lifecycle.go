package westpocket

import (
	"context"

	"connectrpc.com/connect"

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

func checkPocketWrite(ctx context.Context, p *Pocket) error {
	if p.AdminOperationID != "" {
		return failure(connect.CodeFailedPrecondition, "活动正在纠错，请稍后刷新")
	}
	if p.LifecycleRound != requestedRound(ctx) {
		return failure(connect.CodeAborted, "活动已恢复至新轮次，请刷新后重试")
	}
	return nil
}

func (s *Service) lifecycleInterceptor() connect.UnaryInterceptorFunc {
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
				var job Job
				if e := s.DB.NewSelect().Model(&job).Where("id=?", j.GetJobId()).Scan(ctx); e != nil {
					return nil, rpcError(e)
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
			var p Pocket
			if e := s.DB.NewSelect().Model(&p).Where("id=?", pocketID).Scan(ctx); e != nil {
				return nil, rpcError(e)
			}
			ctx = context.WithValue(ctx, pocketRoundKey{}, normalRound(expected))
			if e := checkPocketWrite(ctx, &p); e != nil {
				return nil, e
			}
			return next(ctx, r)
		}
	}
}

func lockAlbumPocket(ctx context.Context, tx bun.Tx, id int64) error {
	var p Pocket
	if e := tx.NewSelect().Model(&p).Where("id=?", id).For("SHARE").Scan(ctx); e != nil {
		return e
	}
	return checkPocketWrite(ctx, &p)
}
