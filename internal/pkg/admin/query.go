package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	adminv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func Page(page, size int32) (int32, int32, error) {
	if page < 0 || page > 1000000 || size < 0 || size > 100 {
		return 0, 0, connect.NewError(connect.CodeInvalidArgument, errors.New("分页参数无效"))
	}
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 20
	}
	return page, size, nil
}

func ParseID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
		return 0, connect.NewError(connect.CodeInvalidArgument, errors.New("记录编号无效"))
	}
	return id, nil
}

func TimeRange(from, to *timestamppb.Timestamp) error {
	if from != nil && !from.IsValid() || to != nil && !to.IsValid() ||
		from != nil && to != nil && from.AsTime().After(to.AsTime()) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("时间范围无效"))
	}
	return nil
}

var resourceNames = map[adminv1.ResourceKind]string{
	adminv1.ResourceKind_RESOURCE_KIND_USER:          "user",
	adminv1.ResourceKind_RESOURCE_KIND_STORE:         "store",
	adminv1.ResourceKind_RESOURCE_KIND_PRODUCT:       "product",
	adminv1.ResourceKind_RESOURCE_KIND_SPOT_GOODS:    "spot_goods",
	adminv1.ResourceKind_RESOURCE_KIND_SPOT_ORDER:    "spot_order",
	adminv1.ResourceKind_RESOURCE_KIND_ERRAND_DEMAND: "errand_demand",
	adminv1.ResourceKind_RESOURCE_KIND_ERRAND_TASK:   "errand_task",
	adminv1.ResourceKind_RESOURCE_KIND_BILL:          "bill",
	adminv1.ResourceKind_RESOURCE_KIND_POCKET:        "pocket",
	adminv1.ResourceKind_RESOURCE_KIND_JOB:           "job",
}

//nolint:gocyclo // Keep filter validation beside the corresponding bounded query predicates.
func ListAudit(
	ctx context.Context,
	db bun.IDB,
	domain string,
	req *adminv1.ListAuditRequest,
) (*adminv1.ListAuditResponse, error) {
	if _, err := Require(ctx); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("缺少查询参数"))
	}
	target, err := table(domain, "admin_action_log")
	if err != nil {
		return nil, err
	}
	page, size, err := Page(req.Page, req.PageSize)
	if err != nil {
		return nil, err
	}
	if err = TimeRange(req.CreatedFrom, req.CreatedTo); err != nil {
		return nil, err
	}
	var rows []struct {
		ID         int64           `bun:"id"`
		ActorID    int64           `bun:"actor_id"`
		Action     string          `bun:"action"`
		TargetType string          `bun:"target_type"`
		TargetID   int64           `bun:"target_id"`
		RequestID  string          `bun:"request_id"`
		Reason     string          `bun:"reason"`
		Evidence   string          `bun:"evidence"`
		Before     json.RawMessage `bun:"before_state"`
		After      json.RawMessage `bun:"after_state"`
		CreatedAt  time.Time       `bun:"created_at"`
	}
	query := db.NewSelect().TableExpr("?", target).Column("*")
	for _, filter := range []struct{ column, value string }{{"actor_id", req.ActorId}, {"target_id", req.ResourceId}} {
		if filter.value != "" {
			id, parseErr := ParseID(filter.value)
			if parseErr != nil {
				return nil, parseErr
			}
			query = query.Where("? = ?", bun.Ident(filter.column), id)
		}
	}
	if req.Resource != adminv1.ResourceKind_RESOURCE_KIND_UNSPECIFIED {
		name, ok := resourceNames[req.Resource]
		if !ok {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("资源类型无效"))
		}
		query = query.Where("target_type = ?", name)
	}
	if req.Action != "" {
		if !validText(req.Action, 100) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("动作类型无效"))
		}
		query = query.Where("action = ?", req.Action)
	}
	if req.RequestId != "" {
		if !validText(req.RequestId, 128) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("请求编号无效"))
		}
		query = query.Where("request_id = ?", req.RequestId)
	}
	if req.CreatedFrom != nil {
		query = query.Where("created_at >= ?", req.CreatedFrom.AsTime())
	}
	if req.CreatedTo != nil {
		query = query.Where("created_at <= ?", req.CreatedTo.AsTime())
	}
	count, err := query.OrderExpr("created_at DESC, id DESC").
		Limit(int(size)).
		Offset(int((page-1)*size)).
		ScanAndCount(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := &adminv1.ListAuditResponse{TotalCount: int64(count)}
	for _, row := range rows {
		kind := adminv1.ResourceKind_RESOURCE_KIND_UNSPECIFIED
		for resource, name := range resourceNames {
			if name == row.TargetType {
				kind = resource
				break
			}
		}
		out.Entries = append(out.Entries, &adminv1.AuditEntry{
			Id:       strconv.FormatInt(row.ID, 10),
			ActorId:  strconv.FormatInt(row.ActorID, 10),
			Resource: kind,
			ResourceId: strconv.FormatInt(
				row.TargetID,
				10,
			),
			Action:    row.Action,
			Reason:    row.Reason,
			Evidence:  row.Evidence,
			RequestId: row.RequestID,
			CreatedAt: timestamppb.New(
				row.CreatedAt,
			),
			Before: snapshotFields(row.Before),
			After:  snapshotFields(row.After),
		})
	}
	return out, nil
}

func snapshotFields(data json.RawMessage) []*adminv1.RecordField {
	var values map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&values) != nil {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var fields []*adminv1.RecordField
	for _, key := range keys {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "secret") ||
			strings.Contains(lower, "password") ||
			strings.Contains(lower, "openid") ||
			strings.Contains(lower, "open_id") ||
			strings.Contains(lower, "verifycode") || strings.Contains(lower, "verify_code") {
			continue
		}
		switch value := values[key].(type) {
		case string, json.Number, bool:
			fields = append(fields, &adminv1.RecordField{Key: key, Label: key, Value: fmt.Sprint(value)})
		}
	}
	return fields
}
