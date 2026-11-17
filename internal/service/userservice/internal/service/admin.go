package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	adminv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/model"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func adminInput(message string) error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New(message))
}

func adminState(message string) error {
	return connect.NewError(connect.CodeFailedPrecondition, errors.New(message))
}

func AdminContext(ctx context.Context) (*adminv1.GetContextResponse, error) {
	actor, err := admin.Require(ctx)
	if err != nil {
		return nil, err
	}
	var user model.UserAccount
	if err = postgres.DB.NewSelect().Model(&user).Where("id = ?", actor.UserID).Scan(ctx); err != nil {
		return nil, err
	}
	version := actor.AuthVersion
	if version == 0 {
		version = 1
	}
	if user.Status != model.MemberStatusActive || user.AuthVersion != version || string(user.Role) != actor.Role {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("账号权限已变更，请重新登录"))
	}
	capabilities := []string{
		"users.read",
		"users.moderate",
		"catalog.manage",
		"spot.manage",
		"errand.manage",
		"payment.manage",
		"westpocket.manage",
		"corrections.execute",
		"audit.read",
	}
	if user.Role == model.MemberRoleSuperadmin {
		capabilities = append(capabilities, "users.manage_roles")
	}
	return &adminv1.GetContextResponse{
		UserId:       strconv.FormatInt(user.ID, 10),
		DisplayName:  user.DisplayName,
		AvatarUrl:    user.AvatarURL,
		Role:         string(user.Role),
		Status:       string(user.Status),
		AuthVersion:  user.AuthVersion,
		Capabilities: capabilities,
	}, nil
}

//nolint:gocyclo // Keep each filter's validation adjacent to its query predicate.
func AdminListUsers(ctx context.Context, req *adminv1.ListRecordsRequest) (*adminv1.ListRecordsResponse, error) {
	actor, err := admin.Require(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil || req.Resource != adminv1.ResourceKind_RESOURCE_KIND_USER {
		return nil, adminInput("仅支持用户资源")
	}
	page, size, err := admin.Page(req.Page, req.PageSize)
	if err != nil {
		return nil, err
	}
	if err = admin.TimeRange(req.CreatedFrom, req.CreatedTo); err != nil {
		return nil, err
	}
	keyword := strings.TrimSpace(req.Keyword)
	if !utf8.ValidString(keyword) || strings.ContainsRune(keyword, '\x00') || utf8.RuneCountInString(keyword) > 100 {
		return nil, adminInput("搜索关键词无效")
	}
	var users []*model.UserAccount
	query := postgres.DB.NewSelect().Model(&users)
	if keyword != "" {
		query = query.Where("(strpos(lower(display_name), lower(?)) > 0 OR id::text = ?)", keyword, keyword)
	}
	if req.UserId != "" {
		id, parseErr := admin.ParseID(req.UserId)
		if parseErr != nil {
			return nil, parseErr
		}
		query = query.Where("id = ?", id)
	}
	if req.Status != "" {
		switch req.Status {
		case "active", "restricted", "banned", "deleted":
		default:
			return nil, adminInput("用户状态无效")
		}
		query = query.Where("status = ?", req.Status)
	}
	if req.CreatedFrom != nil {
		query = query.Where("created_at >= ?", req.CreatedFrom.AsTime())
	}
	if req.CreatedTo != nil {
		query = query.Where("created_at <= ?", req.CreatedTo.AsTime())
	}
	count, err := query.OrderExpr("created_at DESC, id DESC").
		Limit(int(size)).
		Offset(int((page - 1) * size)).
		ScanAndCount(ctx)
	if err != nil {
		return nil, err
	}
	out := &adminv1.ListRecordsResponse{TotalCount: int64(count), Page: page, PageSize: size}
	for _, user := range users {
		out.Records = append(out.Records, adminUserRecord(user, actor.Role))
	}
	return out, nil
}

func AdminGetUser(ctx context.Context, req *adminv1.GetRecordRequest) (*adminv1.GetRecordResponse, error) {
	actor, err := admin.Require(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil || req.Resource != adminv1.ResourceKind_RESOURCE_KIND_USER {
		return nil, adminInput("仅支持用户资源")
	}
	id, err := admin.ParseID(req.Id)
	if err != nil {
		return nil, err
	}
	var user model.UserAccount
	if err = postgres.DB.NewSelect().Model(&user).Where("id = ?", id).Scan(ctx); err != nil {
		return nil, adminUserError(err)
	}
	return &adminv1.GetRecordResponse{Record: adminUserRecord(&user, actor.Role)}, nil
}

func adminUserError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return connect.NewError(connect.CodeNotFound, errors.New("用户不存在"))
	}
	return err
}

func adminUserRecord(user *model.UserAccount, actorRole string) *adminv1.AdminRecord {
	var actions []string
	if actorRole == "superadmin" || user.Role == model.MemberRoleUser {
		if user.Status == model.MemberStatusActive || user.Status == model.MemberStatusRestricted {
			actions = append(actions, "ban")
		}
		if user.Status == model.MemberStatusBanned || user.Status == model.MemberStatusRestricted {
			actions = append(actions, "unban")
		}
	}
	if actorRole == "superadmin" && user.Status == model.MemberStatusActive {
		switch user.Role {
		case model.MemberRoleUser:
			actions = append(actions, "grant_admin", "grant_superadmin")
		case model.MemberRoleAdmin:
			actions = append(actions, "revoke_admin", "grant_superadmin")
		case model.MemberRoleSuperadmin:
			actions = append(actions, "revoke_superadmin")
		}
	}
	return &adminv1.AdminRecord{
		Resource:       adminv1.ResourceKind_RESOURCE_KIND_USER,
		Id:             strconv.FormatInt(user.ID, 10),
		Title:          user.DisplayName,
		Status:         string(user.Status),
		OwnerId:        strconv.FormatInt(user.ID, 10),
		CreatedAt:      timestamppb.New(user.CreatedAt),
		UpdatedAt:      timestamppb.New(user.UpdatedAt),
		LifecycleRound: user.AuthVersion,
		AllowedActions: actions,
		Fields: []*adminv1.RecordField{
			{Key: "role", Label: "角色", Value: string(user.Role)},
			{Key: "auth_version", Label: "权限版本", Value: strconv.FormatInt(user.AuthVersion, 10)},
			{Key: "last_login_at", Label: "最近登录", Value: user.LastLoginAt.UTC().Format(time.RFC3339)},
		},
	}
}

//nolint:gocyclo // Identity locking, last-superadmin protection, deduplication and audit must commit together.
func AdminMutateUser(ctx context.Context, req *adminv1.MutateRecordRequest) (*adminv1.MutateRecordResponse, error) {
	actor, err := admin.Require(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil || req.Resource != adminv1.ResourceKind_RESOURCE_KIND_USER {
		return nil, adminInput("仅支持用户资源")
	}
	id, err := admin.ParseID(req.Id)
	if err != nil {
		return nil, err
	}
	if len(req.Values) != 0 {
		return nil, adminInput("该操作不接受额外字段")
	}
	if req.ExpectedUpdatedAt == nil || !req.ExpectedUpdatedAt.IsValid() || req.ExpectedRound <= 0 {
		return nil, adminInput("缺少有效的用户版本")
	}
	if err = admin.ValidateReason(req.Reason, req.Evidence); err != nil {
		return nil, err
	}
	roleChange := false
	switch req.Action {
	case "ban", "unban":
	case "grant_admin", "revoke_admin", "grant_superadmin", "revoke_superadmin":
		roleChange = true
	default:
		return nil, adminInput("不支持的用户管理操作")
	}
	if roleChange {
		if _, err = admin.RequireSuper(ctx); err != nil {
			return nil, err
		}
	}
	mutation := admin.Mutation{
		Domain:    "user",
		ActorID:   actor.UserID,
		Action:    req.Action,
		RequestID: req.RequestId,
		Input:     req,
	}
	var response *adminv1.MutateRecordResponse
	err = postgres.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(?)", admin.IdentityLock); err != nil {
			return err
		}
		if _, err := admin.RequireCurrent(ctx, tx, roleChange); err != nil {
			return err
		}
		dedup, err := admin.BeginMutation(ctx, tx, mutation)
		if err != nil {
			return err
		}
		if dedup.Replay {
			return json.Unmarshal(dedup.Response, &response)
		}
		var user model.UserAccount
		if err = tx.NewSelect().Model(&user).Where("id = ?", id).For("UPDATE").Scan(ctx); err != nil {
			return adminUserError(err)
		}
		if !user.UpdatedAt.Equal(req.ExpectedUpdatedAt.AsTime()) || user.AuthVersion != req.ExpectedRound {
			return connect.NewError(connect.CodeAborted, errors.New("用户已被修改，请刷新后重试"))
		}
		before := userIdentitySnapshot(&user)
		wasActiveSuper := user.Role == model.MemberRoleSuperadmin && user.Status == model.MemberStatusActive
		if err = applyUserAdminAction(&user, actor.Role, req.Action); err != nil {
			return err
		}
		if wasActiveSuper && (user.Role != model.MemberRoleSuperadmin || user.Status != model.MemberStatusActive) {
			count, err := tx.NewSelect().
				TableExpr(`"user".user_account`).
				Where("role = 'superadmin' AND status = 'active'").
				Count(ctx)
			if err != nil {
				return err
			}
			if count <= 1 {
				return adminState("必须保留至少一名有效的超级管理员")
			}
		}
		user.AuthVersion++
		user.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
		if _, err = tx.NewUpdate().
			Model(&user).
			Column("role", "status", "auth_version", "updated_at").
			WherePK().
			Exec(ctx); err != nil {
			return err
		}
		if err = admin.WriteAudit(
			ctx,
			tx,
			admin.Entry{
				Domain:     "user",
				ActorID:    actor.UserID,
				Action:     req.Action,
				TargetType: "user",
				TargetID:   id,
				RequestID:  req.RequestId,
				Reason:     req.Reason,
				Evidence:   req.Evidence,
				Before:     before,
				After:      userIdentitySnapshot(&user),
			},
		); err != nil {
			return err
		}
		response = &adminv1.MutateRecordResponse{Record: adminUserRecord(&user, actor.Role)}
		return admin.CompleteMutation(ctx, tx, mutation, response)
	})
	return response, err
}

//nolint:gocyclo // The explicit action matrix keeps every privilege transition and its preconditions visible.
func applyUserAdminAction(user *model.UserAccount, actorRole, action string) error {
	if user.Status == model.MemberStatusDeleted {
		return adminState("已删除用户不能执行该操作")
	}
	if actorRole != "superadmin" && user.Role != model.MemberRoleUser {
		return connect.NewError(connect.CodePermissionDenied, errors.New("只有超级管理员可以管理管理员账号"))
	}
	switch action {
	case "ban":
		if user.Status == model.MemberStatusBanned {
			return adminState("用户已经被封禁")
		}
		user.Status = model.MemberStatusBanned
	case "unban":
		if user.Status != model.MemberStatusBanned && user.Status != model.MemberStatusRestricted {
			return adminState("用户当前未被限制")
		}
		user.Status = model.MemberStatusActive
	case "grant_admin":
		if user.Role != model.MemberRoleUser || user.Status != model.MemberStatusActive {
			return adminState("只能为正常普通用户授予管理员")
		}
		user.Role = model.MemberRoleAdmin
	case "revoke_admin":
		if user.Role != model.MemberRoleAdmin {
			return adminState("该用户不是普通管理员")
		}
		user.Role = model.MemberRoleUser
	case "grant_superadmin":
		if user.Role == model.MemberRoleSuperadmin || user.Status != model.MemberStatusActive {
			return adminState("只能为正常非超管用户授予超级管理员")
		}
		user.Role = model.MemberRoleSuperadmin
	case "revoke_superadmin":
		if user.Role != model.MemberRoleSuperadmin {
			return adminState("该用户不是超级管理员")
		}
		user.Role = model.MemberRoleAdmin
	default:
		return adminInput("不支持的用户管理操作")
	}
	return nil
}

func userIdentitySnapshot(user *model.UserAccount) map[string]any {
	return map[string]any{
		"id":           strconv.FormatInt(user.ID, 10),
		"display_name": user.DisplayName,
		"role":         string(user.Role),
		"status":       string(user.Status),
		"auth_version": user.AuthVersion,
	}
}

func BootstrapAdmin(ctx context.Context, db *bun.DB, userID int64, reason, operator string) error {
	if userID <= 0 || strings.TrimSpace(operator) == "" {
		return adminInput("必须指定已核实的用户编号和执行人")
	}
	if err := admin.ValidateReason(reason, operator); err != nil {
		return err
	}
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(?)", admin.IdentityLock); err != nil {
			return err
		}
		var initialized *time.Time
		if err := tx.NewRaw(`SELECT initialized_at FROM "user".admin_bootstrap WHERE singleton FOR UPDATE`).
			Scan(ctx, &initialized); err != nil {
			return err
		}
		if initialized != nil {
			return adminState("超级管理员已初始化，此命令不能再次执行")
		}
		count, err := tx.NewSelect().TableExpr(`"user".user_account`).Where("role = 'superadmin'").Count(ctx)
		if err != nil {
			return err
		}
		if count != 0 {
			return adminState("已有超级管理员，不能执行首次初始化")
		}
		var user model.UserAccount
		if err = tx.NewSelect().Model(&user).Where("id = ?", userID).For("UPDATE").Scan(ctx); err != nil {
			return adminUserError(err)
		}
		if user.Status != model.MemberStatusActive {
			return adminState("只能将正常账号初始化为超级管理员")
		}
		before := userIdentitySnapshot(&user)
		user.Role = model.MemberRoleSuperadmin
		user.AuthVersion++
		user.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
		if _, err = tx.NewUpdate().
			Model(&user).
			Column("role", "auth_version", "updated_at").
			WherePK().
			Exec(ctx); err != nil {
			return err
		}
		if _, err = tx.ExecContext(
			ctx,
			`UPDATE "user".admin_bootstrap SET initialized_at=now(),user_id=? WHERE singleton`,
			userID,
		); err != nil {
			return err
		}
		return admin.WriteAudit(
			ctx,
			tx,
			admin.Entry{
				Domain:     "user",
				ActorID:    userID,
				Action:     "bootstrap_superadmin",
				TargetType: "user",
				TargetID:   userID,
				RequestID:  "bootstrap-superadmin",
				Reason:     reason,
				Evidence:   "运维执行人: " + operator,
				Before:     before,
				After:      userIdentitySnapshot(&user),
			},
		)
	})
}

func AdminUserStats(ctx context.Context) (*adminv1.GetStatsResponse, error) {
	if _, err := admin.Require(ctx); err != nil {
		return nil, err
	}
	var counts struct {
		Total  int64 `bun:"total"`
		Active int64 `bun:"active"`
		Banned int64 `bun:"banned"`
		Admins int64 `bun:"admins"`
	}
	if err := postgres.DB.NewRaw(`SELECT count(*) AS total,count(*) FILTER(WHERE status='active') AS active,count(*) FILTER(WHERE status='banned') AS banned,count(*) FILTER(WHERE role IN ('admin','superadmin')) AS admins FROM "user".user_account`).
		Scan(ctx, &counts); err != nil {
		return nil, err
	}
	return &adminv1.GetStatsResponse{
		Metrics: []*adminv1.Metric{
			{Key: "users", Label: "用户总数", Value: counts.Total},
			{Key: "active_users", Label: "正常用户", Value: counts.Active},
			{Key: "banned_users", Label: "封禁用户", Value: counts.Banned},
			{Key: "admins", Label: "管理员", Value: counts.Admins},
		},
	}, nil
}
