package repository

import (
	"context"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice/internal/model"
	"github.com/uptrace/bun"
)

func Ready(ctx context.Context, db bun.IDB) (bool, error) {
	var exists bool
	err := db.NewRaw("SELECT to_regclass('westpocket.pocket') IS NOT NULL").Scan(ctx, &exists)
	return exists, err
}

func FindPocket(ctx context.Context, db bun.IDB, id int64) (*model.Pocket, error) {
	return findPocket(ctx, db, id, "")
}

func LockPocketForUpdate(ctx context.Context, tx bun.Tx, id int64) (*model.Pocket, error) {
	return findPocket(ctx, tx, id, "UPDATE")
}

func LockPocketForShare(ctx context.Context, tx bun.Tx, id int64) (*model.Pocket, error) {
	return findPocket(ctx, tx, id, "SHARE")
}

func findPocket(ctx context.Context, db bun.IDB, id int64, lock string) (*model.Pocket, error) {
	pocket := new(model.Pocket)
	q := db.NewSelect().Model(pocket).Where("id = ?", id)
	if lock != "" {
		q = q.For(lock)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, err
	}
	return pocket, nil
}

func SavePocket(ctx context.Context, tx bun.Tx, pocket *model.Pocket) error {
	_, err := tx.NewUpdate().Model(pocket).WherePK().Exec(ctx)
	return err
}

func ListMembers(ctx context.Context, db bun.IDB, id int64) ([]model.Member, error) {
	var members []model.Member
	err := db.NewSelect().Model(&members).Where("pocket_id = ?", id).OrderExpr("user_id ASC").Scan(ctx)
	return members, err
}

func ListPockets(
	ctx context.Context,
	db bun.IDB,
	userID int64,
	asOwner bool,
	status string,
	before int64,
	limit int,
) ([]model.Pocket, error) {
	var pockets []model.Pocket
	q := db.NewSelect().Model(&pockets)
	if asOwner {
		q = q.Where("p.owner_id=?", userID)
	} else {
		q = q.Where(
			"p.status<>'draft' AND EXISTS(SELECT 1 FROM westpocket.pocket_member m WHERE m.pocket_id=p.id AND m.user_id=?)",
			userID,
		)
	}
	if status != "" {
		q = q.Where("p.status=?", status)
	}
	if before > 0 {
		q = q.Where("p.id<?", before)
	}
	err := q.OrderExpr("p.id DESC").Limit(limit).Scan(ctx)
	return pockets, err
}

func CountPendingAlbumMembers(ctx context.Context, db bun.IDB, id int64) (int, error) {
	return db.NewSelect().Model((*model.Member)(nil)).Where("pocket_id=? AND album_access<>'accepted'", id).Count(ctx)
}

func ListPhotos(ctx context.Context, db bun.IDB, id int64, album bool) ([]model.Photo, error) {
	var photos []model.Photo
	q := db.NewSelect().
		Model(&photos).
		Where("pocket_id=? AND deleted_at IS NULL AND status NOT IN ('deleted','deleting') AND retention_until>now()", id)
	if album {
		q = q.Where("retention_mode='keepsake'")
	}
	err := q.OrderExpr("id ASC").Scan(ctx)
	return photos, err
}

func ListNotifications(
	ctx context.Context,
	db bun.IDB,
	id, round, recipient int64,
	allRecipients bool,
) ([]model.Notification, error) {
	var notifications []model.Notification
	q := db.NewSelect().Model(&notifications).Where("pocket_id=? AND lifecycle_round=?", id, round)
	if !allRecipients {
		q = q.Where("recipient_user_id=?", recipient)
	}
	err := q.OrderExpr("id ASC").Scan(ctx)
	return notifications, err
}
