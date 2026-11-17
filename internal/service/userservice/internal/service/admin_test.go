package service

import (
	"context"
	"strings"
	"sync"
	"testing"

	adminv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/model"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func adminTestDB(t *testing.T) *bun.DB {
	t.Helper()
	db := testutil.NewPostgres(t, "005_admin_identity.sql")
	previous := postgres.DB
	postgres.DB = db
	t.Cleanup(func() { postgres.DB = previous })
	_, err := db.ExecContext(
		context.Background(),
		`INSERT INTO "user".user_account (id,feishu_open_id,display_name,role,last_login_at) VALUES
	(1,'private-open-id-1','超级管理员甲','superadmin',now()),(2,'private-open-id-2','超级管理员乙','superadmin',now()),
	(3,'private-open-id-3','管理员','admin',now()),(4,'private-open-id-4','普通用户','user',now())`,
	)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func adminTestIdentity(t *testing.T, db *bun.DB, id int64) context.Context {
	t.Helper()
	user := adminTestUser(t, db, id)
	return interceptor.SetUserToContext(
		context.Background(),
		&interceptor.AuthUser{
			UserID:      id,
			Role:        string(user.Role),
			Status:      string(user.Status),
			AuthVersion: user.AuthVersion,
		},
	)
}

func adminTestUser(t *testing.T, db *bun.DB, id int64) *model.UserAccount {
	t.Helper()
	var user model.UserAccount
	if err := db.NewSelect().Model(&user).Where("id = ?", id).Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &user
}

func adminTestMutation(t *testing.T, db *bun.DB, id int64, action, requestID string) *adminv1.MutateRecordRequest {
	t.Helper()
	user := adminTestUser(t, db, id)
	return &adminv1.MutateRecordRequest{
		Resource:          adminv1.ResourceKind_RESOURCE_KIND_USER,
		Id:                adminUserRecord(user, "superadmin").Id,
		Action:            action,
		ExpectedUpdatedAt: timestamppb.New(user.UpdatedAt),
		ExpectedRound:     user.AuthVersion,
		RequestId:         requestID,
		Reason:            "核实后执行管理操作",
		Evidence:          "工单1001",
	}
}

//nolint:gocyclo // Follow the same request through authorization rejection, execution and idempotent replay.
func TestUserAdminAuthorityAndAuditIdempotency(t *testing.T) {
	db := adminTestDB(t)
	regular := adminTestIdentity(t, db, 4)
	operator := adminTestIdentity(t, db, 3)
	super := adminTestIdentity(t, db, 1)
	if _, err := AdminListUsers(
		regular,
		&adminv1.ListRecordsRequest{Resource: adminv1.ResourceKind_RESOURCE_KIND_USER},
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("user list authority: %v", err)
	}
	request := adminTestMutation(t, db, 4, "grant_admin", "promote-once")
	if _, err := AdminMutateUser(operator, request); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("ordinary admin granted role: %v", err)
	}
	if _, err := AdminMutateUser(
		operator,
		adminTestMutation(t, db, 1, "ban", "ban-super"),
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("ordinary admin banned superadmin: %v", err)
	}
	first, err := AdminMutateUser(super, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AdminMutateUser(super, request)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(first, second) {
		t.Fatalf("idempotent response changed: %v %v", first, second)
	}
	if user := adminTestUser(t, db, 4); user.Role != model.MemberRoleAdmin || user.AuthVersion != 2 {
		t.Fatalf("identity not updated: %+v", user)
	}
	changed, ok := proto.Clone(request).(*adminv1.MutateRecordRequest)
	if !ok {
		t.Fatal("unexpected cloned request type")
	}
	changed.Reason = "不同内容"
	if _, err = AdminMutateUser(super, changed); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("changed duplicate accepted: %v", err)
	}
	audit, err := admin.ListAudit(super, db, "user", &adminv1.ListAuditRequest{RequestId: request.RequestId})
	if err != nil {
		t.Fatal(err)
	}
	if audit.TotalCount != 1 || audit.Entries[0].Action != "grant_admin" || audit.Entries[0].ActorId != "1" {
		t.Fatalf("audit=%v", audit)
	}
	list, err := AdminListUsers(
		super,
		&adminv1.ListRecordsRequest{Resource: adminv1.ResourceKind_RESOURCE_KIND_USER, Keyword: "普通用户"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if list.TotalCount != 1 || strings.Contains(list.String(), "private-open-id") {
		t.Fatalf("unexpected projection: %v", list)
	}
}

func TestUserAdminVersionConflictAndAuditFailureRollback(t *testing.T) {
	db := adminTestDB(t)
	ctx := adminTestIdentity(t, db, 1)
	request := adminTestMutation(t, db, 4, "ban", "ban-stale")
	if _, err := db.ExecContext(
		ctx,
		`UPDATE "user".user_account SET updated_at=updated_at+interval '1 second' WHERE id=4`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := AdminMutateUser(ctx, request); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale version accepted: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`ALTER TABLE "user".admin_action_log ADD CONSTRAINT reject_test_reason CHECK(reason <> 'reject-for-test')`,
	); err != nil {
		t.Fatal(err)
	}
	request = adminTestMutation(t, db, 4, "ban", "rollback-audit")
	request.Reason = "reject-for-test"
	if _, err := AdminMutateUser(ctx, request); err == nil {
		t.Fatal("audit failure ignored")
	}
	user := adminTestUser(t, db, 4)
	if user.Status != model.MemberStatusActive || user.AuthVersion != 1 {
		t.Fatalf("business change committed without audit: %+v", user)
	}
	count, err := db.NewSelect().TableExpr(`"user".admin_request_dedup`).Count(ctx)
	if err != nil || count != 0 {
		t.Fatalf("failed mutation retained dedup row: count=%d err=%v", count, err)
	}
}

func TestLastActiveSuperadminIsProtectedAcrossConcurrentDemotions(t *testing.T) {
	db := adminTestDB(t)
	contexts := []context.Context{adminTestIdentity(t, db, 1), adminTestIdentity(t, db, 2)}
	requests := []*adminv1.MutateRecordRequest{
		adminTestMutation(t, db, 1, "revoke_superadmin", "self-demote-a"),
		adminTestMutation(t, db, 2, "revoke_superadmin", "self-demote-b"),
	}
	var group sync.WaitGroup
	errorsOut := make(chan error, 2)
	start := make(chan struct{})
	for i := range requests {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			_, err := AdminMutateUser(contexts[index], requests[index])
			errorsOut <- err
		}(i)
	}
	close(start)
	group.Wait()
	close(errorsOut)
	successes, blocked := 0, 0
	for err := range errorsOut {
		if err == nil {
			successes++
		} else if connect.CodeOf(err) == connect.CodeFailedPrecondition {
			blocked++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || blocked != 1 {
		t.Fatalf("successes=%d blocked=%d", successes, blocked)
	}
	count, err := db.NewSelect().
		TableExpr(`"user".user_account`).
		Where("role='superadmin' AND status='active'").
		Count(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("active superadmins=%d err=%v", count, err)
	}
}

func TestBootstrapAdminIsOneTimeAndConcurrentSafe(t *testing.T) {
	db := testutil.NewPostgres(t, "005_admin_identity.sql")
	ctx := context.Background()
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO "user".user_account(id,feishu_open_id,display_name) VALUES (1,'bootstrap-one','甲'),(2,'bootstrap-two','乙')`,
	); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	results := make(chan error, 2)
	for id := int64(1); id <= 2; id++ {
		group.Add(1)
		go func(userID int64) {
			defer group.Done()
			results <- BootstrapAdmin(ctx, db, userID, "首次配置", "test operator")
		}(id)
	}
	group.Wait()
	close(results)
	successes, blocked := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if connect.CodeOf(err) == connect.CodeFailedPrecondition {
			blocked++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || blocked != 1 {
		t.Fatalf("successes=%d blocked=%d", successes, blocked)
	}
	if _, err := db.ExecContext(ctx, `UPDATE "user".user_account SET role='user'`); err != nil {
		t.Fatal(err)
	}
	if err := BootstrapAdmin(
		ctx,
		db,
		1,
		"不能再次初始化",
		"test operator",
	); connect.CodeOf(
		err,
	) != connect.CodeFailedPrecondition {
		t.Fatalf("bootstrap marker was ignored: %v", err)
	}
}
