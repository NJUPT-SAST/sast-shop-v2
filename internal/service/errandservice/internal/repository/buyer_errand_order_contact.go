package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
)

var ErrNotFound = errors.New("not found")

type BuyerErrandOrderContactRecord struct {
	RequesterID int64  `bun:"requester_id"`
	CaptainID   *int64 `bun:"captain_id"`
}

func GetBuyerErrandOrderContactRecord(
	ctx context.Context,
	errandDemandID int64,
) (*BuyerErrandOrderContactRecord, error) {
	var record BuyerErrandOrderContactRecord
	err := postgres.DB.NewSelect().
		TableExpr("errand.errand_demand AS ed").
		ColumnExpr("ed.requester_id AS requester_id").
		ColumnExpr("et.captain_id AS captain_id").
		Join("LEFT JOIN errand.errand_task AS et ON et.id = ed.task_id").
		Where("ed.id = ?", errandDemandID).
		Scan(ctx, &record)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get buyer errand order contact: %w", err)
	}
	return &record, nil
}
