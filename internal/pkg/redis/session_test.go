package redis

import (
	"context"
	"testing"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/testutil"
)

func TestSessionIdentityRevocationSurvivesUnbanAndRejectsOldRole(t *testing.T) {
	db := testutil.NewPostgres(t, "005_admin_identity.sql")
	ctx := context.Background()
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO "user".user_account (id,feishu_open_id,display_name,role) VALUES (1,'session-test','用户','admin')`,
	); err != nil {
		t.Fatal(err)
	}
	store := &SessionStore{DB: db}
	legacy := &interceptor.AuthUser{UserID: 1, Role: "admin", Status: "active", AccessToken: "preserved-feishu-token"}
	current, err := store.resolveSession(ctx, legacy)
	if err != nil || current.AuthVersion != 1 || current.AccessToken != legacy.AccessToken {
		t.Fatalf("legacy session: %#v %v", current, err)
	}
	for _, statement := range []string{
		`UPDATE "user".user_account SET status='banned',auth_version=auth_version+1 WHERE id=1`,
		`UPDATE "user".user_account SET status='active',auth_version=auth_version+1 WHERE id=1`,
	} {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
		if _, err = store.resolveSession(ctx, legacy); err == nil {
			t.Fatal("revoked legacy session was accepted")
		}
	}
	fresh := &interceptor.AuthUser{UserID: 1, Role: "admin", Status: "active", AuthVersion: 3}
	if _, err = store.resolveSession(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(
		ctx,
		`UPDATE "user".user_account SET role='user',auth_version=auth_version+1 WHERE id=1`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err = store.resolveSession(ctx, fresh); err == nil {
		t.Fatal("old administrator session survived demotion")
	}
	if _, err = store.resolveSession(
		ctx,
		&interceptor.AuthUser{UserID: 1, Role: "admin", Status: "active", AuthVersion: 4},
	); err == nil {
		t.Fatal("forged role accepted with current version")
	}
	current, err = store.GetUserByID(ctx, 1)
	if err != nil || current.Role != "user" || current.AuthVersion != 4 {
		t.Fatalf("development identity not authoritative: %#v %v", current, err)
	}
}

func TestSessionIdentityFailsClosedOnMissingUserOrDatabase(t *testing.T) {
	db := testutil.NewPostgres(t, "005_admin_identity.sql")
	store := &SessionStore{DB: db}
	for _, snapshot := range []*interceptor.AuthUser{nil, {UserID: 0}, {UserID: 99, Role: "admin", Status: "active", AuthVersion: 1}} {
		if _, err := store.resolveSession(context.Background(), snapshot); err == nil {
			t.Fatalf("invalid session accepted: %#v", snapshot)
		}
	}
	if _, err := db.ExecContext(context.Background(), `DROP TABLE "user".user_account CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.resolveSession(
		context.Background(),
		&interceptor.AuthUser{UserID: 99, Role: "admin", Status: "active", AuthVersion: 1},
	); err == nil {
		t.Fatal("identity failure accepted")
	}
}
