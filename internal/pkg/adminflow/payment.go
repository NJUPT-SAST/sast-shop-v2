package adminflow

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	ac "buf.build/gen/go/sast/sast-shop-v2/connectrpc/go/sast/sastshopv2/admin/v1/adminv1connect"
	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/config"
)

const InternalHeader = "X-Admin-Internal-Token"

func paymentClient() (ac.PaymentCorrectionInternalServiceClient, string, error) {
	token := os.Getenv("ADMIN_INTERNAL_TOKEN")
	if len(token) < 32 || config.AppConfig == nil {
		return nil, "", Failure(connect.CodeFailedPrecondition, "管理员跨服务纠错尚未配置")
	}
	endpoint := fmt.Sprintf("%s:%d", config.AppConfig.PaymentServiceURL, config.AppConfig.PaymentServicePort)
	return ac.NewPaymentCorrectionInternalServiceClient(&http.Client{Timeout: 20 * time.Second}, endpoint), token, nil
}

func GetSourceBills(ctx context.Context, source string, id int64, round int64) ([]*av.SourceBill, error) {
	actor, err := admin.Require(ctx)
	if err != nil {
		return nil, err
	}
	client, token, err := paymentClient()
	if err != nil {
		return nil, err
	}
	req := connect.NewRequest(
		&av.GetSourceBillsRequest{
			SourceType:     source,
			SourceId:       strconv.FormatInt(id, 10),
			LifecycleRound: round,
			ActorId:        strconv.FormatInt(actor.UserID, 10),
		},
	)
	req.Header().Set(InternalHeader, token)
	response, err := client.GetSourceBills(ctx, req)
	if err != nil {
		return nil, err
	}
	return response.Msg.Bills, nil
}

func CorrectBills(
	ctx context.Context,
	c *Case,
	source string,
	targetRound int64,
	bills []*av.SourceBill,
	funds string,
	restoration ...[]*av.SourceBill,
) (*av.CorrectSourceBillsResponse, error) {
	actor, err := admin.Require(ctx)
	if err != nil {
		return nil, err
	}
	client, token, err := paymentClient()
	if err != nil {
		return nil, err
	}
	req := connect.NewRequest(
		&av.CorrectSourceBillsRequest{
			OperationId:   c.ID + ":payment",
			ActorId:       strconv.FormatInt(actor.UserID, 10),
			SourceType:    source,
			SourceId:      strconv.FormatInt(c.ResourceID, 10),
			ExpectedRound: c.Plan.Request.ExpectedRound,
			TargetRound:   targetRound,
			Action:        c.Action,
			ExpectedBills: bills,
			Reason:        c.Plan.Request.Reason,
			Evidence:      c.Plan.Request.Evidence,
			FundsHandling: funds,
		},
	)
	if len(restoration) > 0 {
		req.Msg.RestorationBills = restoration[0]
	}
	req.Msg.SubmittedUnreceivedConfirmed = c.Plan.Request.Values["submitted_unreceived_confirmed"] == "true"
	req.Header().Set(InternalHeader, token)
	response, err := client.CorrectSourceBills(ctx, req)
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}
