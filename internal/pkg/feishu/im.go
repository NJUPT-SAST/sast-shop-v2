package feishu

import (
	"context"
	"encoding/json"
	"fmt"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func SendTextByOpenID(ctx context.Context, openID string, text string, bizKey string) (*SendMessageResult, error) {
	if bizKey != "" {
		if ok, err := AcquireMessageDedupe(ctx, bizKey); err != nil {
			return nil, err
		} else if !ok {
			return &SendMessageResult{}, nil
		}
	}

	client, err := getClient()
	if err != nil {
		return nil, err
	}

	contentBytes, err := json.Marshal(map[string]string{
		"text": text,
	})
	if err != nil {
		return nil, err
	}

	bodyBuilder := larkim.NewCreateMessageReqBodyBuilder().
		ReceiveId(openID).
		MsgType("text").
		Content(string(contentBytes))
	if bizKey != "" {
		bodyBuilder.Uuid(bizKey)
	}

	resp, err := client.SDK.Im.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.CreateMessageV1ReceiveIDTypeOpenId).
		Body(bodyBuilder.Build()).
		Build(),
	)
	if err != nil {
		return nil, mapFeishuError(err)
	}
	if resp == nil {
		return nil, fmt.Errorf("feishu send message response is empty")
	}
	if !resp.Success() {
		return nil, &APIError{
			Code:    resp.Code,
			Message: resp.Msg,
		}
	}
	if resp.Data == nil {
		return nil, fmt.Errorf("feishu send message data is empty")
	}

	return &SendMessageResult{
		MessageID: larkcore.StringValue(resp.Data.MessageId),
	}, nil
}
