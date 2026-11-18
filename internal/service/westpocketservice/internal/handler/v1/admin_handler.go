package v1

import (
	"context"

	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice/internal/service"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice/internal/support"
)

type AdminHandler struct{ S *service.Service }

func (h *AdminHandler) ListRecords(
	ctx context.Context,
	r *connect.Request[av.ListRecordsRequest],
) (*connect.Response[av.ListRecordsResponse], error) {
	out, e := h.S.AdminList(ctx, r.Msg)
	if e != nil {
		return nil, support.RPCError(e)
	}
	return connect.NewResponse(out), nil
}

func (h *AdminHandler) GetRecord(
	ctx context.Context,
	r *connect.Request[av.GetRecordRequest],
) (*connect.Response[av.GetRecordResponse], error) {
	out, e := h.S.AdminGet(ctx, r.Msg)
	if e != nil {
		return nil, support.RPCError(e)
	}
	return connect.NewResponse(out), nil
}

func (h *AdminHandler) MutateRecord(
	ctx context.Context,
	r *connect.Request[av.MutateRecordRequest],
) (*connect.Response[av.MutateRecordResponse], error) {
	out, e := h.S.AdminMutate(ctx, r.Msg)
	if e != nil {
		return nil, support.RPCError(e)
	}
	return connect.NewResponse(out), nil
}

func (h *AdminHandler) PreviewCorrection(
	ctx context.Context,
	r *connect.Request[av.PreviewCorrectionRequest],
) (*connect.Response[av.PreviewCorrectionResponse], error) {
	out, e := h.S.AdminEngine().Preview(ctx, r.Msg)
	if e != nil {
		return nil, support.RPCError(e)
	}
	return connect.NewResponse(out), nil
}

func (h *AdminHandler) ExecuteCorrection(
	ctx context.Context,
	r *connect.Request[av.ExecuteCorrectionRequest],
) (*connect.Response[av.ExecuteCorrectionResponse], error) {
	out, e := h.S.AdminEngine().Execute(ctx, r.Msg)
	if e != nil {
		return nil, support.RPCError(e)
	}
	return connect.NewResponse(out), nil
}

func (h *AdminHandler) GetCorrection(
	ctx context.Context,
	r *connect.Request[av.GetCorrectionRequest],
) (*connect.Response[av.GetCorrectionResponse], error) {
	out, e := h.S.AdminEngine().Get(ctx, r.Msg)
	if e != nil {
		return nil, support.RPCError(e)
	}
	return connect.NewResponse(out), nil
}

func (h *AdminHandler) ListCorrections(
	ctx context.Context,
	r *connect.Request[av.ListCorrectionsRequest],
) (*connect.Response[av.ListCorrectionsResponse], error) {
	out, e := h.S.AdminEngine().List(ctx, r.Msg)
	if e != nil {
		return nil, support.RPCError(e)
	}
	return connect.NewResponse(out), nil
}

func (h *AdminHandler) ListAudit(
	ctx context.Context,
	r *connect.Request[av.ListAuditRequest],
) (*connect.Response[av.ListAuditResponse], error) {
	out, e := admin.ListAudit(ctx, h.S.DB, "westpocket", r.Msg)
	if e != nil {
		return nil, support.RPCError(e)
	}
	return connect.NewResponse(out), nil
}

func (h *AdminHandler) GetStats(
	ctx context.Context,
	r *connect.Request[av.GetStatsRequest],
) (*connect.Response[av.GetStatsResponse], error) {
	out, e := h.S.AdminStats(ctx)
	if e != nil {
		return nil, support.RPCError(e)
	}
	return connect.NewResponse(out), nil
}
