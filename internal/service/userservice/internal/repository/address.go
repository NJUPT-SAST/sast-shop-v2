package repository

import (
	"context"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/model"
	"github.com/uptrace/bun"
)

// CreateAddress inserts a new address. Pass a *bun.DB or bun.Tx as db.
func CreateAddress(ctx context.Context, db bun.IDB, address *model.MemberAddress) error {
	_, err := db.NewInsert().Model(address).Exec(ctx)
	return err
}

// GetAddressByID returns a single address by its primary key.
func GetAddressByID(ctx context.Context, addressID int64) (*model.MemberAddress, error) {
	var address model.MemberAddress
	err := postgres.DB.NewSelect().Model(&address).Where("id = ?", addressID).Scan(ctx)
	return &address, err
}

// GetAddressesByUserID returns all addresses for a user, default address first.
func GetAddressesByUserID(ctx context.Context, userID int64) ([]*model.MemberAddress, error) {
	var addresses []*model.MemberAddress
	err := postgres.DB.NewSelect().
		Model(&addresses).
		Where("user_id = ?", userID).
		Order("is_default DESC", "id ASC").
		Scan(ctx)
	return addresses, err
}

// UpdateAddress updates the mutable fields of an address by its primary key.
func UpdateAddress(ctx context.Context, db bun.IDB, address *model.MemberAddress) error {
	address.UpdatedAt = time.Now()
	_, err := db.NewUpdate().
		Model(address).
		Column("recipient_name", "recipient_phone", "province", "city", "district", "detail_address", "is_default", "updated_at").
		WherePK().
		Exec(ctx)
	return err
}

// DeleteAddress removes an address by its primary key.
func DeleteAddress(ctx context.Context, db bun.IDB, addressID int64) error {
	_, err := db.NewDelete().
		Model((*model.MemberAddress)(nil)).
		Where("id = ?", addressID).
		Exec(ctx)
	return err
}

// ClearDefaultAddressByUserID sets is_default = false on all addresses for the given user.
func ClearDefaultAddressByUserID(ctx context.Context, db bun.IDB, userID int64) error {
	_, err := db.NewUpdate().
		Model((*model.MemberAddress)(nil)).
		Set("is_default = ?", false).
		Set("updated_at = ?", time.Now()).
		Where("user_id = ? AND is_default = ?", userID, true).
		Exec(ctx)
	return err
}
