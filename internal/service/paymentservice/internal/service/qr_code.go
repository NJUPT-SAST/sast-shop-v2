package service

import (
	"context"

	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/paymentservice/internal/repository"
)

// GetQrCode returns the list of QR codes owned by the given user.
func GetQrCode(ctx context.Context, ownerID int64) ([]*paymentv1.QrCode, error) {
	qrCodes, err := repository.GetQRCodesByOwnerID(ctx, ownerID)
	if err != nil {
		return nil, err
	}

	result := make([]*paymentv1.QrCode, 0, len(qrCodes))
	for _, qr := range qrCodes {
		ch, ok := model.ModelChannelToProto(qr.Channel)
		if !ok {
			continue
		}
		result = append(result, &paymentv1.QrCode{
			Id:      qr.ID,
			Channel: ch,
			Content: qr.Content,
		})
	}
	return result, nil
}

// UpdateQrCode upserts a QR code for the given user and channel.
func UpdateQrCode(ctx context.Context, ownerID int64, channel paymentv1.Channel, content string) (*paymentv1.QrCode, error) {
	ch, ok := model.ProtoChannelToModel(channel)
	if !ok {
		return nil, ErrInvalidChannel
	}

	qrCode := &model.PaymentQRCode{
		OwnerID: ownerID,
		Channel: ch,
		Content: content,
	}

	result, err := repository.UpsertQRCode(ctx, qrCode)
	if err != nil {
		return nil, err
	}

	protoChannel, _ := model.ModelChannelToProto(result.Channel)
	return &paymentv1.QrCode{
		Id:      result.ID,
		Channel: protoChannel,
		Content: result.Content,
	}, nil
}
