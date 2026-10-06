package service

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"

	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/userservice/internal/repository"
	"github.com/rs/zerolog/log"
	"github.com/uptrace/bun"
)

var ErrAddressNotFound = errors.New("address not found")

var recipientPhonePattern = regexp.MustCompile(`^1[3-9][0-9]{9}$`)

func validateAddressFields(name, phone, province, city, district, detail string) error {
	if strings.TrimSpace(name) == "" || !recipientPhonePattern.MatchString(phone) ||
		strings.TrimSpace(province) == "" || strings.TrimSpace(city) == "" ||
		strings.TrimSpace(district) == "" || strings.TrimSpace(detail) == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("收件人、手机号或地址信息不正确"))
	}
	return nil
}

// CreateAddress creates a new address for the given user.
func CreateAddress(
	ctx context.Context,
	userID int64,
	req *userv1.CreateAddressRequest,
) (*userv1.ShippingAddress, error) {
	if userID <= 0 || req == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("地址请求不正确"))
	}
	if err := validateAddressFields(
		req.RecipientName,
		req.RecipientPhone,
		req.Province,
		req.City,
		req.District,
		req.DetailAddress,
	); err != nil {
		return nil, err
	}
	address := &model.MemberAddress{
		UserID:         userID,
		RecipientName:  req.RecipientName,
		RecipientPhone: req.RecipientPhone,
		Province:       req.Province,
		City:           req.City,
		District:       req.District,
		DetailAddress:  req.DetailAddress,
		IsDefault:      req.IsDefault,
	}

	err := postgres.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if address.IsDefault {
			if err := repository.ClearDefaultAddressByUserID(ctx, tx, userID); err != nil {
				return err
			}
		}
		return repository.CreateAddress(ctx, tx, address)
	})
	if err != nil {
		log.Error().Err(err).Msgf("Failed to create address for userID: %d", userID)
		return nil, internalError()
	}

	return modelAddressToProto(address), nil
}

// UpdateAddress updates an existing address. The userID ensures users can only modify their own addresses.
func UpdateAddress(
	ctx context.Context,
	userID int64,
	req *userv1.UpdateAddressRequest,
) (*userv1.ShippingAddress, error) {
	if userID <= 0 || req == nil || req.AddressId <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("地址请求不正确"))
	}
	if err := validateAddressFields(
		req.RecipientName,
		req.RecipientPhone,
		req.Province,
		req.City,
		req.District,
		req.DetailAddress,
	); err != nil {
		return nil, err
	}
	existing, err := repository.GetAddressByID(ctx, req.AddressId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAddressNotFound
		}
		log.Error().Err(err).Msgf("Failed to get address for update, addressID: %d", req.AddressId)
		return nil, internalError()
	}

	if existing.UserID != userID {
		return nil, ErrAddressNotFound
	}

	existing.RecipientName = req.RecipientName
	existing.RecipientPhone = req.RecipientPhone
	existing.Province = req.Province
	existing.City = req.City
	existing.District = req.District
	existing.DetailAddress = req.DetailAddress
	existing.IsDefault = req.IsDefault

	err = postgres.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if existing.IsDefault {
			if err := repository.ClearDefaultAddressByUserID(ctx, tx, userID); err != nil {
				return err
			}
		}
		return repository.UpdateAddress(ctx, tx, existing)
	})
	if err != nil {
		log.Error().Err(err).Msgf("Failed to update address, addressID: %d", req.AddressId)
		return nil, internalError()
	}

	return modelAddressToProto(existing), nil
}

// GetAddress returns addresses for the given user. If addressID is non-zero, returns a single address.
func GetAddress(ctx context.Context, userID int64, addressID *int64) ([]*userv1.ShippingAddress, error) {
	if userID <= 0 || (addressID != nil && *addressID <= 0) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("地址 ID 不正确"))
	}
	if addressID != nil {
		address, err := repository.GetAddressByID(ctx, *addressID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrAddressNotFound
			}
			log.Error().Err(err).Msgf("Failed to get address, addressID: %d", *addressID)
			return nil, internalError()
		}
		if address.UserID != userID {
			return nil, ErrAddressNotFound
		}
		return []*userv1.ShippingAddress{modelAddressToProto(address)}, nil
	}

	addresses, err := repository.GetAddressesByUserID(ctx, userID)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to get addresses for userID: %d", userID)
		return nil, internalError()
	}

	result := make([]*userv1.ShippingAddress, 0, len(addresses))
	for _, a := range addresses {
		result = append(result, modelAddressToProto(a))
	}
	return result, nil
}

// DeleteAddress deletes an address. The userID ensures users can only delete their own addresses.
func DeleteAddress(ctx context.Context, userID int64, addressID int64) error {
	if userID <= 0 || addressID <= 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("地址 ID 不正确"))
	}
	existing, err := repository.GetAddressByID(ctx, addressID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAddressNotFound
		}
		log.Error().Err(err).Msgf("Failed to get address for delete, addressID: %d", addressID)
		return internalError()
	}

	if existing.UserID != userID {
		return ErrAddressNotFound
	}

	err = repository.DeleteAddress(ctx, postgres.DB, addressID)
	if err != nil {
		log.Error().Err(err).Msgf("Failed to delete address, addressID: %d", addressID)
		return internalError()
	}
	return nil
}

func modelAddressToProto(a *model.MemberAddress) *userv1.ShippingAddress {
	return &userv1.ShippingAddress{
		Id:             a.ID,
		RecipientName:  a.RecipientName,
		RecipientPhone: a.RecipientPhone,
		Province:       a.Province,
		City:           a.City,
		District:       a.District,
		DetailAddress:  a.DetailAddress,
		IsDefault:      a.IsDefault,
	}
}

func internalError() error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_UserError{
		UserError: &userv1.UserError{
			Code: userv1.UserErrorCode_USER_ERROR_CODE_INTERNAL_ERROR,
		},
	}, "")
}
