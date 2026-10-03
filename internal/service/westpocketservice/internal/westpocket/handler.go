package westpocket

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	wpconnect "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/westpocket/v1/westpocketv1connect"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	wp "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/westpocket/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct{ S *Service }

var (
	_ wpconnect.WestPocketServiceHandler         = (*Handler)(nil)
	_ wpconnect.FaceProfileServiceHandler        = (*Handler)(nil)
	_ wpconnect.WestPocketInternalServiceHandler = (*Handler)(nil)
)

func rpcError(e error) error {
	if e == nil {
		return nil
	}
	var c *connect.Error
	if errors.As(e, &c) {
		return e
	}
	if errors.Is(e, sql.ErrNoRows) {
		return failure(connect.CodeNotFound, "资源不存在")
	}
	return failure(connect.CodeInternal, "暂时无法完成操作，请稍后重试")
}
func actor(ctx context.Context) (int64, error) { return interceptor.UserIDFromContext(ctx) }
func timestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

func userProto(u User) *userv1.UserInfo {
	return &userv1.UserInfo{Id: u.ID, Name: u.Name, AvatarUrl: u.AvatarURL}
}

func pocketProto(p *Pocket, actor int64) *wp.Pocket {
	if p == nil {
		return nil
	}
	return &wp.Pocket{
		Id:               p.ID,
		OwnerId:          p.OwnerID,
		Title:            p.Title,
		TotalCents:       p.TotalCents,
		Status:           p.Status,
		Revision:         p.Revision,
		ParticipantCount: p.ParticipantCount,
		OwnerShareCents:  p.OwnerShareCents,
		ReceivableCents:  p.ReceivableCents,
		CreatedAt:        timestamppb.New(p.CreatedAt),
		UpdatedAt:        timestamppb.New(p.UpdatedAt),
		PublishedAt:      timestamp(p.PublishedAt),
		CancelReason:     p.CancelReason,
		IsOwner:          p.OwnerID == actor,
	}
}

func profileProto(p *Profile, count int) *wp.FaceProfile {
	if p == nil {
		return nil
	}
	return &wp.FaceProfile{
		Id:               p.ID,
		Status:           p.Status,
		Revision:         p.Revision,
		SampleCount:      boundedInt32(count),
		ConsentVersion:   p.ConsentVersion,
		ConsentedAt:      timestamppb.New(p.ConsentedAt),
		ConsentExpiresAt: timestamppb.New(p.ConsentExpiresAt),
		RevokedAt:        timestamp(p.RevokedAt),
	}
}

func jobProto(j *Job) *wp.Job {
	if j == nil {
		return nil
	}
	var progress struct {
		Total     int32 `json:"total"`
		Completed int32 `json:"completed"`
		Failed    int32 `json:"failed"`
	}
	if err := json.Unmarshal(j.Progress, &progress); err != nil {
		observeError(err)
	}
	return &wp.Job{
		Id:             j.ID,
		Kind:           j.Kind,
		Status:         j.Status,
		TotalItems:     progress.Total,
		CompletedItems: progress.Completed,
		FailedItems:    progress.Failed,
		ErrorCode:      j.ErrorCode,
		Retryable:      j.Status == "failed" || j.Status == "partial_failed",
		UpdatedAt:      timestamppb.New(j.UpdatedAt),
	}
}

func notificationsProto(n []Notification) []*wp.Notification {
	out := make([]*wp.Notification, 0, len(n))
	for _, v := range n {
		out = append(
			out,
			&wp.Notification{
				Id:              v.ID,
				RecipientUserId: v.RecipientUserID,
				Kind:            v.Kind,
				Status:          v.Status,
				ErrorCode:       v.ErrorCode,
			},
		)
	}
	return out
}

func uploadIDs(ids []string) ([]int64, error) {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		v, e := strconv.ParseInt(id, 10, 64)
		if e != nil || v <= 0 {
			return nil, failure(connect.CodeInvalidArgument, "上传编号无效")
		}
		out = append(out, v)
	}
	return out, nil
}

func (h *Handler) membersProto(ctx context.Context, p *Pocket, m []Member) ([]*wp.PocketMember, error) {
	ids := make([]int64, 0, len(m))
	billIDs := []int64{}
	for _, v := range m {
		ids = append(ids, v.UserID)
		if v.PaymentBillID != nil {
			billIDs = append(billIDs, *v.PaymentBillID)
		}
	}
	users, e := h.S.Directory.GetUsers(ctx, ids)
	if e != nil {
		return nil, e
	}
	mapping := map[int64]*userv1.UserInfo{}
	for _, u := range users {
		mapping[u.ID] = userProto(u)
	}
	bills, e := h.S.Payments.Bills(ctx, billIDs)
	if e != nil {
		return nil, e
	}
	versions := map[int64]Bill{}
	for _, b := range bills {
		versions[b.ID] = b
	}
	out := make([]*wp.PocketMember, 0, len(m))
	for _, v := range m {
		a := &wp.PocketMember{
			Id:              v.ID,
			UserId:          v.UserID,
			User:            mapping[v.UserID],
			SelectionSource: v.SelectionSource,
			FaceMatchId:     value(v.FaceMatchID),
			ShareCents:      v.ShareCents,
			PaymentBillId:   value(v.PaymentBillID),
			BillStatus:      v.BillStatus,
			IsOwner:         v.UserID == p.OwnerID,
			AlbumAccess:     v.AlbumAccess,
		}
		if b, ok := versions[a.PaymentBillId]; ok {
			a.BillStatus = b.Status
			a.BillUpdatedAt = timestamppb.New(b.UpdatedAt)
		}
		out = append(out, a)
	}
	return out, nil
}

func (h *Handler) pocket(ctx context.Context, uid, id int64) (*wp.Pocket, error) {
	p, _, e := h.S.Pocket(ctx, uid, id)
	if e != nil {
		return nil, e
	}
	out := pocketProto(p, uid)
	users, e := h.S.Directory.GetUsers(ctx, []int64{p.OwnerID})
	if e != nil {
		return nil, e
	}
	if len(users) > 0 {
		out.Owner = userProto(users[0])
	}
	return out, nil
}

func (h *Handler) photosProto(ctx context.Context, photos []Photo) ([]*wp.PocketPhoto, error) {
	out := make([]*wp.PocketPhoto, 0, len(photos))
	for _, p := range photos {
		url := ""
		if p.ObjectKey != "" && h.S.Storage != nil {
			var e error
			url, e = h.S.Storage.URL(ctx, p.ObjectKey)
			if e != nil {
				return nil, e
			}
		}
		out = append(
			out,
			&wp.PocketPhoto{
				Id:                     p.ID,
				PocketId:               p.PocketID,
				PreviewUrl:             url,
				Status:                 p.Status,
				Width:                  boundedInt32(p.Width),
				Height:                 boundedInt32(p.Height),
				DetectedFaceCount:      p.DetectedFaceCount,
				RetentionMode:          p.RetentionMode,
				RetentionUntil:         timestamppb.New(p.RetentionUntil),
				ErrorCode:              p.ErrorCode,
				LatestRecognitionJobId: value(p.LatestJobID),
			},
		)
	}
	return out, nil
}

func (h *Handler) matchesProto(ctx context.Context, matches []Match) ([]*wp.FaceMatch, error) {
	ids := []int64{}
	seen := map[int64]bool{}
	for _, m := range matches {
		for _, c := range m.Candidates {
			if !seen[c.UserID] {
				ids = append(ids, c.UserID)
				seen[c.UserID] = true
			}
		}
	}
	users, e := h.S.Directory.GetUsers(ctx, ids)
	if e != nil {
		return nil, e
	}
	lookup := map[int64]*userv1.UserInfo{}
	for _, u := range users {
		lookup[u.ID] = userProto(u)
	}
	out := make([]*wp.FaceMatch, 0, len(matches))
	for _, m := range matches {
		v := &wp.FaceMatch{
			Id:               m.ID,
			PhotoId:          m.PhotoID,
			RecognitionJobId: m.JobID,
			FaceIndex:        boundedInt32(m.FaceIndex),
			BboxX:            boundedInt32(m.BboxX),
			BboxY:            boundedInt32(m.BboxY),
			BboxWidth:        boundedInt32(m.BboxWidth),
			BboxHeight:       boundedInt32(m.BboxHeight),
			SuggestedUserId:  value(m.SuggestedUserID),
			ConfirmedUserId:  value(m.ConfirmedUserID),
			Score:            m.Score,
			MatchStatus:      m.MatchStatus,
			Resolution:       m.Resolution,
		}
		if v.SuggestedUserId > 0 && lookup[v.SuggestedUserId] == nil {
			v.SuggestedUserId = 0
			v.MatchStatus = "unknown"
			v.Score = 0
		}
		for _, c := range m.Candidates {
			if u := lookup[c.UserID]; u != nil {
				v.Candidates = append(v.Candidates, &wp.FaceCandidate{User: u, Score: c.Score})
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func (h *Handler) faceAndJob(ctx context.Context, uid, id int64) (*wp.FaceProfile, *wp.Job, error) {
	p, count, e := h.S.Profile(ctx, uid)
	if e != nil {
		return nil, nil, e
	}
	j, e := h.S.GetJob(ctx, uid, id)
	if e != nil {
		return nil, nil, e
	}
	profile := profileProto(p, count)
	profile.JobId = id
	return profile, jobProto(j), nil
}

func (h *Handler) GetCollectionState(
	ctx context.Context,
	r *connect.Request[wp.GetCollectionStateRequest],
) (*connect.Response[wp.GetCollectionStateResponse], error) {
	if e := interceptor.RequireWestPocketService(r.Header()); e != nil {
		return nil, e
	}
	p, m, e := h.S.internalPocket(ctx, r.Msg.PocketId)
	if e != nil {
		return nil, rpcError(e)
	}
	out := &wp.GetCollectionStateResponse{Status: p.Status, OwnerId: p.OwnerID}
	for _, a := range m {
		out.Members = append(
			out.Members,
			&wp.PocketMember{
				Id:            a.ID,
				UserId:        a.UserID,
				ShareCents:    a.ShareCents,
				PaymentBillId: value(a.PaymentBillID),
				IsOwner:       a.UserID == p.OwnerID,
			},
		)
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) GetCapabilities(
	ctx context.Context,
	r *connect.Request[wp.GetCapabilitiesRequest],
) (*connect.Response[wp.GetCapabilitiesResponse], error) {
	return connect.NewResponse(
		&wp.GetCapabilitiesResponse{
			FaceRecognitionAvailable: h.S.Faces != nil && h.S.Storage != nil,
			PhotoUploadAvailable:     h.S.Storage != nil,
			NotificationsAvailable:   h.S.Messenger != nil,
			FaceConsentVersion:       h.S.FacePolicy,
			PhotoConsentVersion:      h.S.PhotoPolicy,
			MaxPhotos:                10,
			MaxMembers:               50,
			MaxUploadBytes:           MaxUploadBytes,
		},
	), nil
}

func (h *Handler) GetMyFaceProfile(
	ctx context.Context,
	r *connect.Request[wp.GetMyFaceProfileRequest],
) (*connect.Response[wp.GetMyFaceProfileResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	p, count, e := h.S.Profile(ctx, uid)
	if e != nil {
		return nil, rpcError(e)
	}
	out := profileProto(p, count)
	if p != nil {
		j := new(Job)
		if e = h.S.DB.NewSelect().
			Model(j).
			Where("profile_id=?", p.ID).
			OrderExpr("id DESC").
			Limit(1).
			Scan(ctx); e == nil {
			out.JobId = j.ID
		}
	}
	return connect.NewResponse(&wp.GetMyFaceProfileResponse{FaceProfile: out}), nil
}

func (h *Handler) EnrollMyFace(
	ctx context.Context,
	r *connect.Request[wp.EnrollMyFaceRequest],
) (*connect.Response[wp.EnrollMyFaceResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	ids, e := uploadIDs(r.Msg.UploadIds)
	if e != nil {
		return nil, e
	}
	id, e := h.S.Enroll(ctx, uid, 0, ids, r.Msg.ConsentVersion, r.Msg.RequestId, false)
	if e != nil {
		return nil, rpcError(e)
	}
	p, j, e := h.faceAndJob(ctx, uid, id)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&wp.EnrollMyFaceResponse{FaceProfile: p, Job: j}), nil
}

func (h *Handler) ReplaceMyFace(
	ctx context.Context,
	r *connect.Request[wp.ReplaceMyFaceRequest],
) (*connect.Response[wp.ReplaceMyFaceResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	ids, e := uploadIDs(r.Msg.UploadIds)
	if e != nil {
		return nil, e
	}
	id, e := h.S.Enroll(ctx, uid, r.Msg.ExpectedRevision, ids, r.Msg.ConsentVersion, r.Msg.RequestId, true)
	if e != nil {
		return nil, rpcError(e)
	}
	p, j, e := h.faceAndJob(ctx, uid, id)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&wp.ReplaceMyFaceResponse{FaceProfile: p, Job: j}), nil
}

func (h *Handler) RevokeMyFace(
	ctx context.Context,
	r *connect.Request[wp.RevokeMyFaceRequest],
) (*connect.Response[wp.RevokeMyFaceResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	id, e := h.S.Revoke(ctx, uid, r.Msg.ExpectedRevision, r.Msg.RequestId)
	if e != nil {
		return nil, rpcError(e)
	}
	p, j, e := h.faceAndJob(ctx, uid, id)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&wp.RevokeMyFaceResponse{FaceProfile: p, Job: j}), nil
}

func (h *Handler) CreatePocket(
	ctx context.Context,
	r *connect.Request[wp.CreatePocketRequest],
) (*connect.Response[wp.CreatePocketResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	id, e := h.S.Create(ctx, uid, r.Msg.Title, r.Msg.TotalCents, r.Msg.RequestId)
	if e != nil {
		return nil, rpcError(e)
	}
	p, e := h.pocket(ctx, uid, id)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&wp.CreatePocketResponse{Pocket: p}), nil
}

func (h *Handler) UpdatePocket(
	ctx context.Context,
	r *connect.Request[wp.UpdatePocketRequest],
) (*connect.Response[wp.UpdatePocketResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	id, e := h.S.Update(
		ctx,
		uid,
		r.Msg.PocketId,
		r.Msg.ExpectedRevision,
		r.Msg.Title,
		r.Msg.TotalCents,
		r.Msg.RequestId,
	)
	if e != nil {
		return nil, rpcError(e)
	}
	p, e := h.pocket(ctx, uid, id)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&wp.UpdatePocketResponse{Pocket: p}), nil
}

func (h *Handler) GetPocket(
	ctx context.Context,
	r *connect.Request[wp.GetPocketRequest],
) (*connect.Response[wp.GetPocketResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	id := r.Msg.PocketId
	if _, _, e = h.S.Pocket(ctx, uid, id); e != nil {
		return nil, rpcError(e)
	}
	if e = h.S.Refresh(ctx, id); e != nil {
		return nil, rpcError(e)
	}
	p, m, e := h.S.Pocket(ctx, uid, id)
	if e != nil {
		return nil, rpcError(e)
	}
	out := &wp.GetPocketResponse{IsOwner: p.OwnerID == uid}
	if out.Pocket, e = h.pocket(ctx, uid, id); e != nil {
		return nil, rpcError(e)
	}
	if out.Members, e = h.membersProto(ctx, p, m); e != nil {
		return nil, rpcError(e)
	}
	photos, e := h.S.Photos(ctx, uid, id, false)
	if e != nil {
		return nil, rpcError(e)
	}
	if out.Photos, e = h.photosProto(ctx, photos); e != nil {
		return nil, rpcError(e)
	}
	if out.IsOwner {
		var jobs []Job
		if e = h.S.DB.NewSelect().
			Model(&jobs).
			Where("pocket_id=?", id).
			OrderExpr("id DESC").
			Limit(20).
			Scan(ctx); e != nil {
			return nil, rpcError(e)
		}
		for i := range jobs {
			out.Jobs = append(out.Jobs, jobProto(&jobs[i]))
		}
	}
	n, e := h.S.Notifications(ctx, uid, id)
	if e != nil {
		return nil, rpcError(e)
	}
	out.Notifications = notificationsProto(n)
	return connect.NewResponse(out), nil
}

func (h *Handler) ListMyPockets(
	ctx context.Context,
	r *connect.Request[wp.ListMyPocketsRequest],
) (*connect.Response[wp.ListMyPocketsResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	pockets, next, e := h.S.List(ctx, uid, r.Msg.Perspective, r.Msg.Status, int(r.Msg.PageSize), r.Msg.PageToken)
	if e != nil {
		return nil, rpcError(e)
	}
	out := &wp.ListMyPocketsResponse{NextPageToken: next}
	for i := range pockets {
		out.Pockets = append(out.Pockets, pocketProto(&pockets[i], uid))
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) AddPocketPhotos(
	ctx context.Context,
	r *connect.Request[wp.AddPocketPhotosRequest],
) (*connect.Response[wp.AddPocketPhotosResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	ids, e := uploadIDs(r.Msg.UploadIds)
	if e != nil {
		return nil, e
	}
	id, e := h.S.AddPhotos(
		ctx,
		uid,
		r.Msg.PocketId,
		r.Msg.ExpectedRevision,
		ids,
		r.Msg.RetentionMode,
		r.Msg.CaptureAuthorizationVersion,
		r.Msg.RequestId,
	)
	if e != nil {
		return nil, rpcError(e)
	}
	p, e := h.pocket(ctx, uid, id)
	if e != nil {
		return nil, rpcError(e)
	}
	photos, e := h.S.Photos(ctx, uid, id, false)
	if e != nil {
		return nil, rpcError(e)
	}
	ph, e := h.photosProto(ctx, photos)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&wp.AddPocketPhotosResponse{Pocket: p, Photos: ph}), nil
}

func (h *Handler) DeletePocketPhoto(
	ctx context.Context,
	r *connect.Request[wp.DeletePocketPhotoRequest],
) (*connect.Response[wp.DeletePocketPhotoResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	_, e = h.S.DeletePhoto(ctx, uid, r.Msg.PocketId, r.Msg.PhotoId, r.Msg.RequestId)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&wp.DeletePocketPhotoResponse{}), nil
}

func (h *Handler) StartRecognition(
	ctx context.Context,
	r *connect.Request[wp.StartRecognitionRequest],
) (*connect.Response[wp.StartRecognitionResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	id, e := h.S.StartRecognition(ctx, uid, r.Msg.PocketId, r.Msg.PhotoIds, r.Msg.RequestId)
	if e != nil {
		return nil, rpcError(e)
	}
	j, e := h.S.GetJob(ctx, uid, id)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&wp.StartRecognitionResponse{Job: jobProto(j)}), nil
}

func (h *Handler) GetJob(
	ctx context.Context,
	r *connect.Request[wp.GetJobRequest],
) (*connect.Response[wp.GetJobResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	j, e := h.S.GetJob(ctx, uid, r.Msg.JobId)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&wp.GetJobResponse{Job: jobProto(j)}), nil
}

func (h *Handler) RetryJob(
	ctx context.Context,
	r *connect.Request[wp.RetryJobRequest],
) (*connect.Response[wp.RetryJobResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	id, e := h.S.Retry(ctx, uid, r.Msg.JobId, r.Msg.RequestId)
	if e != nil {
		return nil, rpcError(e)
	}
	j, e := h.S.GetJob(ctx, uid, id)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&wp.RetryJobResponse{Job: jobProto(j)}), nil
}

func (h *Handler) GetRecognitionResults(
	ctx context.Context,
	r *connect.Request[wp.GetRecognitionResultsRequest],
) (*connect.Response[wp.GetRecognitionResultsResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	m, e := h.S.Matches(ctx, uid, r.Msg.PocketId, r.Msg.JobId)
	if e != nil {
		return nil, rpcError(e)
	}
	matches, e := h.matchesProto(ctx, m)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&wp.GetRecognitionResultsResponse{Matches: matches}), nil
}

func (h *Handler) ResolveFaceMatch(
	ctx context.Context,
	r *connect.Request[wp.ResolveFaceMatchRequest],
) (*connect.Response[wp.ResolveFaceMatchResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	id, e := h.S.Resolve(
		ctx,
		uid,
		r.Msg.PocketId,
		r.Msg.FaceMatchId,
		r.Msg.UserId,
		r.Msg.ExpectedRevision,
		r.Msg.Ignore,
		r.Msg.RequestId,
	)
	if e != nil {
		return nil, rpcError(e)
	}
	p, e := h.pocket(ctx, uid, id)
	if e != nil {
		return nil, rpcError(e)
	}
	matches, e := h.S.Matches(ctx, uid, id, 0)
	if e != nil {
		return nil, rpcError(e)
	}
	out := &wp.ResolveFaceMatchResponse{Pocket: p}
	for _, m := range matches {
		if m.ID == r.Msg.FaceMatchId {
			values, e := h.matchesProto(ctx, []Match{m})
			if e != nil {
				return nil, rpcError(e)
			}
			out.Match = values[0]
		}
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) SearchParticipants(
	ctx context.Context,
	r *connect.Request[wp.SearchParticipantsRequest],
) (*connect.Response[wp.SearchParticipantsResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	users, next, e := h.S.Search(ctx, uid, r.Msg.PocketId, r.Msg.Query, int(r.Msg.PageSize), r.Msg.PageToken)
	if e != nil {
		return nil, rpcError(e)
	}
	out := &wp.SearchParticipantsResponse{NextPageToken: next}
	for _, u := range users {
		out.Users = append(out.Users, userProto(u))
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) ReplaceMembers(
	ctx context.Context,
	r *connect.Request[wp.ReplaceMembersRequest],
) (*connect.Response[wp.ReplaceMembersResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	m := make([]Member, 0, len(r.Msg.Members))
	for _, v := range r.Msg.Members {
		member := Member{UserID: v.UserId, SelectionSource: v.SelectionSource}
		if v.FaceMatchId > 0 {
			member.FaceMatchID = ptr(v.FaceMatchId)
		}
		m = append(m, member)
	}
	id, e := h.S.ReplaceMembers(ctx, uid, r.Msg.PocketId, r.Msg.ExpectedRevision, m, r.Msg.RequestId)
	if e != nil {
		return nil, rpcError(e)
	}
	p, mem, e := h.S.Pocket(ctx, uid, id)
	if e != nil {
		return nil, rpcError(e)
	}
	members, e := h.membersProto(ctx, p, mem)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&wp.ReplaceMembersResponse{Pocket: pocketProto(p, uid), Members: members}), nil
}

func (h *Handler) PreviewSplit(
	ctx context.Context,
	r *connect.Request[wp.PreviewSplitRequest],
) (*connect.Response[wp.PreviewSplitResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	p, m, e := h.S.Preview(ctx, uid, r.Msg.PocketId, r.Msg.ExpectedRevision)
	if e != nil {
		return nil, rpcError(e)
	}
	members, e := h.membersProto(ctx, p, m)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(
		&wp.PreviewSplitResponse{
			PocketId:         p.ID,
			Revision:         p.Revision,
			TotalCents:       p.TotalCents,
			ParticipantCount: p.ParticipantCount,
			OwnerShareCents:  p.OwnerShareCents,
			ReceivableCents:  p.ReceivableCents,
			Members:          members,
		},
	), nil
}

func (h *Handler) PublishPocket(
	ctx context.Context,
	r *connect.Request[wp.PublishPocketRequest],
) (*connect.Response[wp.PublishPocketResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	id, e := h.S.Publish(ctx, uid, r.Msg.PocketId, r.Msg.ExpectedRevision, r.Msg.RequestId)
	if e != nil {
		return nil, rpcError(e)
	}
	p, e := h.pocket(ctx, uid, r.Msg.PocketId)
	if e != nil {
		return nil, rpcError(e)
	}
	j, e := h.S.GetJob(ctx, uid, id)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&wp.PublishPocketResponse{Pocket: p, Job: jobProto(j)}), nil
}

func (h *Handler) GetMyPayment(
	ctx context.Context,
	r *connect.Request[wp.GetMyPaymentRequest],
) (*connect.Response[wp.GetMyPaymentResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	p, b, qr, e := h.S.Snapshot(ctx, uid, r.Msg.PocketId)
	if e != nil {
		return nil, rpcError(e)
	}
	users, e := h.S.Directory.GetUsers(ctx, []int64{p.OwnerID})
	if e != nil {
		return nil, rpcError(e)
	}
	out := &wp.GetMyPaymentResponse{Pocket: pocketProto(p, uid), QrContent: qr.Content, IsOwner: uid == p.OwnerID}
	if len(users) > 0 {
		out.Payee = userProto(users[0])
		out.Pocket.Owner = out.Payee
	}
	if b != nil {
		out.Bill = b.Proto
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) CancelPocket(
	ctx context.Context,
	r *connect.Request[wp.CancelPocketRequest],
) (*connect.Response[wp.CancelPocketResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	id, e := h.S.Cancel(ctx, uid, r.Msg.PocketId, r.Msg.ExpectedRevision, r.Msg.Reason, r.Msg.RequestId)
	if e != nil {
		return nil, rpcError(e)
	}
	p, e := h.pocket(ctx, uid, r.Msg.PocketId)
	if e != nil {
		return nil, rpcError(e)
	}
	out := &wp.CancelPocketResponse{Pocket: p}
	if id > 0 {
		j, e := h.S.GetJob(ctx, uid, id)
		if e != nil {
			return nil, rpcError(e)
		}
		out.Job = jobProto(j)
	}
	return connect.NewResponse(out), nil
}

func (h *Handler) RemindMembers(
	ctx context.Context,
	r *connect.Request[wp.RemindMembersRequest],
) (*connect.Response[wp.RemindMembersResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	if e = h.S.Remind(ctx, uid, r.Msg.PocketId, r.Msg.MemberIds, r.Msg.RequestId); e != nil {
		return nil, rpcError(e)
	}
	n, e := h.S.Notifications(ctx, uid, r.Msg.PocketId)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&wp.RemindMembersResponse{Notifications: notificationsProto(n)}), nil
}

func (h *Handler) SetAlbumAccess(
	ctx context.Context,
	r *connect.Request[wp.SetAlbumAccessRequest],
) (*connect.Response[wp.SetAlbumAccessResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	if e = h.S.SetAlbum(ctx, uid, r.Msg.PocketId, r.Msg.Accepted, r.Msg.RequestId); e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&wp.SetAlbumAccessResponse{}), nil
}

func (h *Handler) ListAlbumPhotos(
	ctx context.Context,
	r *connect.Request[wp.ListAlbumPhotosRequest],
) (*connect.Response[wp.ListAlbumPhotosResponse], error) {
	uid, e := actor(ctx)
	if e != nil {
		return nil, e
	}
	photos, e := h.S.Photos(ctx, uid, r.Msg.PocketId, true)
	if e != nil {
		return nil, rpcError(e)
	}
	scope := "album:" + strconv.FormatInt(r.Msg.PocketId, 10)
	after, e := h.S.parseCursor(r.Msg.PageToken, uid, scope)
	if e != nil {
		return nil, e
	}
	filtered := []Photo{}
	for _, ph := range photos {
		if ph.ID > after {
			filtered = append(filtered, ph)
		}
	}
	size := int(r.Msg.PageSize)
	if size <= 0 || size > 20 {
		size = 20
	}
	next := ""
	if len(filtered) > size {
		filtered = filtered[:size]
		next = h.S.cursor(uid, filtered[len(filtered)-1].ID, scope)
	}
	values, e := h.photosProto(ctx, filtered)
	if e != nil {
		return nil, rpcError(e)
	}
	return connect.NewResponse(&wp.ListAlbumPhotosResponse{Photos: values, NextPageToken: next}), nil
}
