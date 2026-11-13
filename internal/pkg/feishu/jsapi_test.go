package feishu

import (
	"context"
	"crypto/sha1" //nolint:gosec
	"encoding/hex"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/constant"
	shopredis "github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/redis"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	goredis "github.com/redis/go-redis/v9"
)

func TestSignURLUsesMillisecondsInSignature(t *testing.T) {
	previousClient, previousRedis := AppClient, shopredis.Client
	cache := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	cache.AddHook(jsapiTicketTestHook{t: t})
	shopredis.Client = cache
	AppClient = &Client{
		AppID: "test-app",
		SDK:   lark.NewClient("test-app", "test-secret"),
	}
	t.Cleanup(func() {
		AppClient, shopredis.Client = previousClient, previousRedis
		if err := cache.Close(); err != nil {
			t.Errorf("close test cache: %v", err)
		}
	})

	before := time.Now().UnixMilli()
	signature, err := SignURL(context.Background(), "https://shop.example.com/shop?store=1#item")
	after := time.Now().UnixMilli()
	if err != nil {
		t.Fatalf("SignURL: %v", err)
	}
	timestamp, err := strconv.ParseInt(signature.Timestamp, 10, 64)
	if err != nil {
		t.Fatalf("parse timestamp: %v", err)
	}
	if timestamp < before || timestamp > after {
		t.Fatalf("timestamp %d must be current milliseconds within [%d, %d]", timestamp, before, after)
	}
	if signature.AppID != "test-app" || signature.URL != "https://shop.example.com/shop?store=1" {
		t.Fatalf("unexpected app ID or signing URL: %q, %q", signature.AppID, signature.URL)
	}
	raw := fmt.Sprintf("jsapi_ticket=test-ticket&noncestr=%s&timestamp=%s&url=%s",
		signature.NonceStr, signature.Timestamp, signature.URL)
	digest := sha1.Sum([]byte(raw)) //nolint:gosec
	if signature.Signature != hex.EncodeToString(digest[:]) {
		t.Fatal("signature must use the same millisecond timestamp returned to the client")
	}
}

type jsapiTicketTestHook struct {
	t *testing.T
}

func (jsapiTicketTestHook) DialHook(next goredis.DialHook) goredis.DialHook {
	return next
}

func (h jsapiTicketTestHook) ProcessHook(_ goredis.ProcessHook) goredis.ProcessHook {
	return func(_ context.Context, cmd goredis.Cmder) error {
		if cmd.Name() != "get" || len(cmd.Args()) != 2 || cmd.Args()[1] != constant.FeishuJSAPITicketKey {
			h.t.Fatalf("unexpected cache command: %v", cmd.Args())
		}
		stringCommand, ok := cmd.(*goredis.StringCmd)
		if !ok {
			h.t.Fatalf("unexpected cache command type: %T", cmd)
		}
		stringCommand.SetVal(`{"ticket":"test-ticket","expire_in":7200}`)
		return nil
	}
}

func (jsapiTicketTestHook) ProcessPipelineHook(next goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return next
}
