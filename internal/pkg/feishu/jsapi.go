package feishu

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/rs/zerolog/log"
)

type jsapiTicketResponseData struct {
	Ticket   string `json:"ticket"`
	ExpireIn int32  `json:"expire_in"`
}

func GetJSAPITicket(ctx context.Context) (*JSAPITicket, error) {
	if cached, err := GetCachedJSAPITicket(ctx); err == nil && cached != nil {
		return cached, nil
	}

	client, err := getClient()
	if err != nil {
		return nil, err
	}

	rawResp, err := client.SDK.Post(ctx, "/open-apis/jssdk/ticket/get", map[string]any{}, larkcore.AccessTokenTypeTenant)
	if err != nil {
		return nil, mapFeishuError(err)
	}

	var resp openAPIResponse[jsapiTicketResponseData]
	if err := json.Unmarshal(rawResp.RawBody, &resp); err != nil {
		return nil, err
	}
	if err := resp.Err(); err != nil {
		return nil, err
	}

	ticket := &JSAPITicket{
		Ticket:   resp.Data.Ticket,
		ExpireIn: resp.Data.ExpireIn,
	}
	if err := SetCachedJSAPITicket(ctx, ticket); err != nil {
		log.Warn().Err(err).Msg("feishu: failed to cache jsapi ticket")
	}
	return ticket, nil
}

func SignURL(ctx context.Context, requestURL string) (*JSAPISignature, error) {
	client, err := getClient()
	if err != nil {
		return nil, err
	}

	// 飞书 H5 JSAPI 签名要求 URL 不含 # 及之后片段。
	requestURL = strings.SplitN(requestURL, "#", 2)[0]

	ticket, err := GetJSAPITicket(ctx)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	nonceStr := hex.EncodeToString(buf)

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	raw := fmt.Sprintf(
		"jsapi_ticket=%s&noncestr=%s&timestamp=%s&url=%s",
		ticket.Ticket,
		nonceStr,
		timestamp,
		requestURL,
	)

	sum := sha1.Sum([]byte(raw))
	signature := hex.EncodeToString(sum[:])

	return &JSAPISignature{
		AppID:     client.AppID,
		NonceStr:  nonceStr,
		Timestamp: timestamp,
		Signature: signature,
		URL:       requestURL,
	}, nil
}
