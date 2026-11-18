package repository

import (
	"context"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/westpocketservice/internal/model"
	"github.com/uptrace/bun"
)

func FindJob(ctx context.Context, db bun.IDB, id int64) (*model.Job, error) {
	job := new(model.Job)
	if err := db.NewSelect().Model(job).Where("id=?", id).Scan(ctx); err != nil {
		return nil, err
	}
	return job, nil
}

func LockJob(ctx context.Context, tx bun.Tx, id int64) (*model.Job, error) {
	job := new(model.Job)
	if err := tx.NewSelect().Model(job).Where("id=?", id).For("UPDATE").Scan(ctx); err != nil {
		return nil, err
	}
	return job, nil
}

func LatestProfileJob(ctx context.Context, db bun.IDB, profileID int64) (*model.Job, error) {
	job := new(model.Job)
	err := db.NewSelect().Model(job).Where("profile_id=?", profileID).OrderExpr("id DESC").Limit(1).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return job, nil
}

func ListPocketJobs(ctx context.Context, db bun.IDB, pocketID int64, limit int) ([]model.Job, error) {
	var jobs []model.Job
	err := db.NewSelect().Model(&jobs).Where("pocket_id=?", pocketID).OrderExpr("id DESC").Limit(limit).Scan(ctx)
	return jobs, err
}
