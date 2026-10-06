package feishu

import (
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec
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
	// 先查缓存，避免频繁请求飞书
	if cached, err := GetCachedJSAPITicket(ctx); err == nil && cached != nil {
		return cached, nil
	}
	// 获取客户端
	client, err := getClient()
	if err != nil {
		return nil, err
	}
	// 使用 tenant_access_token 鉴权，向飞书的 /open-apis/jssdk/ticket/get 发起 POST 请求
	rawResp, err := client.SDK.Post(
		ctx, "/open-apis/jssdk/ticket/get", map[string]any{}, larkcore.AccessTokenTypeTenant,
	)
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
	// 把票据写入缓存
	if err := SetCachedJSAPITicket(ctx, ticket); err != nil {
		log.Warn().Err(err).Msg("feishu: failed to cache jsapi ticket")
	}
	return ticket, nil
}

// 为页面url生成签名，生成前端调用wx.config时需要的appid，ttl，nonceStr等参数
func SignURL(ctx context.Context, requestURL string) (*JSAPISignature, error) {
	client, err := getClient()
	if err != nil {
		return nil, err
	}

	// 飞书 H5 JSAPI 签名要求 URL 不含 # 及之后片段。
	// 去除url的hash片段（如 #/page）
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

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	raw := fmt.Sprintf(
		"jsapi_ticket=%s&noncestr=%s&timestamp=%s&url=%s",
		ticket.Ticket,
		nonceStr,
		timestamp,
		requestURL,
	)

	sum := sha1.Sum([]byte(raw)) //nolint:gosec
	signature := hex.EncodeToString(sum[:])

	return &JSAPISignature{
		AppID:     client.AppID,
		NonceStr:  nonceStr,
		Timestamp: timestamp,
		Signature: signature,
		URL:       requestURL,
	}, nil
}
