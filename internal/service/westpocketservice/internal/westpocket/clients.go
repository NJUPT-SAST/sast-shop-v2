package westpocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	paymentconnect "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/payment/v1/paymentv1connect"
	userconnect "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/user/v1/userv1connect"
	paymentv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/payment/v1"
	userv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/user/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/connect/interceptor"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/feishu"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type RPCDirectory struct {
	client userconnect.UserInternalServiceClient
}
type RPCPayments struct {
	client paymentconnect.PaymentInternalServiceClient
}

func NewClients(userURL, paymentURL, token string) (*RPCDirectory, *RPCPayments) {
	auth := connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if token == "" {
				return nil, ErrUnavailable
			}
			req.Header().Set(interceptor.WestPocketServiceHeader, token)
			return next(ctx, req)
		}
	}))
	httpClient := &http.Client{Timeout: 20 * time.Second}
	return &RPCDirectory{
			userconnect.NewUserInternalServiceClient(httpClient, userURL, auth),
		}, &RPCPayments{
			paymentconnect.NewPaymentInternalServiceClient(httpClient, paymentURL, auth),
		}
}

func (d *RPCDirectory) GetUsers(ctx context.Context, ids []int64) ([]User, error) {
	out := make([]User, 0, len(ids))
	for start := 0; start < len(ids); start += 100 {
		end := min(start+100, len(ids))
		r, e := d.client.GetUsers(ctx, connect.NewRequest(&userv1.GetUsersRequest{UserIds: ids[start:end]}))
		if e != nil {
			return nil, e
		}
		for _, u := range r.Msg.Users {
			out = append(out, User{ID: u.Id, Name: u.Name, AvatarURL: u.AvatarUrl})
		}
	}
	return out, nil
}

func (d *RPCDirectory) Search(ctx context.Context, query string, size int, token string) ([]User, string, error) {
	r, e := d.client.SearchUsers(
		ctx,
		connect.NewRequest(&userv1.SearchUsersRequest{Query: query, PageSize: boundedInt32(size), PageToken: token}),
	)
	if e != nil {
		return nil, "", e
	}
	out := make([]User, 0, len(r.Msg.Users))
	for _, u := range r.Msg.Users {
		out = append(out, User{ID: u.Id, Name: u.Name, AvatarURL: u.AvatarUrl})
	}
	return out, r.Msg.NextPageToken, nil
}

func (d *RPCDirectory) OpenID(ctx context.Context, id int64) (string, error) {
	r, e := d.client.GetUserContactOpenID(ctx, connect.NewRequest(&userv1.GetUserContactOpenIDRequest{UserId: id}))
	if e != nil {
		return "", e
	}
	if r.Msg.FeishuOpenId == "" {
		return "", ErrUnavailable
	}
	return r.Msg.FeishuOpenId, nil
}

func (p *RPCPayments) QR(ctx context.Context, owner int64) (QR, error) {
	r, e := p.client.GetPayeeQrCode(ctx, connect.NewRequest(&paymentv1.GetPayeeQrCodeRequest{OwnerId: owner}))
	if e != nil {
		return QR{}, e
	}
	return QR{ID: r.Msg.Id, Content: r.Msg.Content, UpdatedAt: r.Msg.UpdatedAt.AsTime()}, nil
}

func billFromProto(b *paymentv1.Bill) (Bill, error) {
	if b == nil || b.Payer == nil || b.Payee == nil {
		return Bill{}, ErrUnavailable
	}
	status := map[paymentv1.BillStatus]string{paymentv1.BillStatus_BILL_STATUS_UNPAID: "unpaid", paymentv1.BillStatus_BILL_STATUS_SUBMITTED: "submitted", paymentv1.BillStatus_BILL_STATUS_COMPLETED: "completed", paymentv1.BillStatus_BILL_STATUS_CLOSED: "closed"}[b.Status]
	if status == "" {
		return Bill{}, ErrState
	}
	return Bill{
		ID:          b.Id,
		PayerID:     b.Payer.Id,
		PayeeID:     b.Payee.Id,
		SourceID:    b.GetSourceId(),
		AmountCents: b.AmountCents,
		SourceType:  b.GetSourceType(),
		Status:      status,
		BillNo:      b.BillNo,
		VerifyCode:  b.VerifyCode,
		UpdatedAt:   b.UpdatedAt.AsTime(),
		Proto:       b,
	}, nil
}

func (p *RPCPayments) Create(ctx context.Context, id, payer, payee int64, amount int32) (Bill, error) {
	r, e := p.client.CreateBillForOrder(
		ctx,
		connect.NewRequest(
			&paymentv1.CreateBillForOrderRequest{
				SourceType:  "west_pocket",
				SourceId:    id,
				PayerId:     payer,
				PayeeId:     payee,
				AmountCents: amount,
			},
		),
	)
	if e != nil {
		return Bill{}, e
	}
	return billFromProto(r.Msg.Bill)
}

func (p *RPCPayments) Bills(ctx context.Context, ids []int64) ([]Bill, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	r, e := p.client.BatchGetBills(ctx, connect.NewRequest(&paymentv1.BatchGetBillsRequest{BillIds: ids}))
	if e != nil {
		return nil, e
	}
	out := make([]Bill, 0, len(r.Msg.Bills))
	for _, b := range r.Msg.Bills {
		v, e := billFromProto(b)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}

func (p *RPCPayments) Cancel(ctx context.Context, id int64) error {
	_, e := p.client.CancelWestPocketBillsIfUnpaid(
		ctx,
		connect.NewRequest(&paymentv1.CancelWestPocketBillsIfUnpaidRequest{PocketId: id}),
	)
	return e
}

type FeishuMessenger struct{}

func (m *FeishuMessenger) Send(ctx context.Context, openID, uuid, text, link string, png []byte) (string, error) {
	if !feishuReady() {
		return "", ErrUnavailable
	}
	client := feishu.AppClient.SDK
	msgType := "interactive"
	var content []byte
	var e error
	if len(png) > 0 {
		imageResp, err := client.Im.Image.Create(
			ctx,
			larkim.NewCreateImageReqBuilder().
				Body(larkim.NewCreateImageReqBodyBuilder().ImageType("message").Image(bytes.NewReader(png)).Build()).
				Build(),
		)
		if err != nil {
			return "", err
		}
		if imageResp == nil || !imageResp.Success() || imageResp.Data == nil || imageResp.Data.ImageKey == nil {
			return "", errors.New("FEISHU_IMAGE_FAILED")
		}
		msgType = "image"
		content, e = json.Marshal(map[string]string{"image_key": *imageResp.Data.ImageKey})
	} else {
		content, e = json.Marshal(
			map[string]any{
				"config": map[string]any{"wide_screen_mode": true},
				"header": map[string]any{"title": map[string]string{"tag": "plain_text", "content": "West Pocket"}},
				"elements": []any{
					map[string]any{"tag": "div", "text": map[string]string{"tag": "plain_text", "content": text}},
					map[string]any{
						"tag": "action",
						"actions": []any{
							map[string]any{
								"tag":  "button",
								"text": map[string]string{"tag": "plain_text", "content": "查看账单"},
								"type": "primary",
								"url":  link,
							},
						},
					},
				},
			},
		)
	}
	if e != nil {
		return "", e
	}
	resp, e := client.Im.Message.Create(
		ctx,
		larkim.NewCreateMessageReqBuilder().
			ReceiveIdType("open_id").
			Body(larkim.NewCreateMessageReqBodyBuilder().ReceiveId(openID).MsgType(msgType).Content(string(content)).Uuid(uuid).Build()).
			Build(),
	)
	if e != nil {
		return "", e
	}
	if resp == nil || !resp.Success() || resp.Data == nil || resp.Data.MessageId == nil || *resp.Data.MessageId == "" {
		return "", errors.New("FEISHU_SEND_FAILED")
	}
	return *resp.Data.MessageId, nil
}

func feishuReady() bool { return feishu.AppClient != nil && feishu.AppClient.SDK != nil }
