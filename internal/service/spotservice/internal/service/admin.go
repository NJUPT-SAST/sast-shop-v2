package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	adminv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/search"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func ListAdminRecords(ctx context.Context, req *adminv1.ListRecordsRequest) (*adminv1.ListRecordsResponse, error) {
	if _, err := admin.Require(ctx); err != nil {
		return nil, err
	}
	page, size, err := admin.Page(req.Page, req.PageSize)
	if err != nil {
		return nil, err
	}
	keyword, err := search.NormalizeKeyword(req.Keyword)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var owner int64
	if req.UserId != "" {
		owner, err = admin.ParseID(req.UserId)
		if err != nil {
			return nil, err
		}
	}
	if err := admin.TimeRange(req.CreatedFrom, req.CreatedTo); err != nil {
		return nil, err
	}
	out := &adminv1.ListRecordsResponse{Page: page, PageSize: size}
	switch req.Resource {
	case adminv1.ResourceKind_RESOURCE_KIND_SPOT_GOODS:
		err = listAdminGoods(ctx, req, keyword, owner, out)
	case adminv1.ResourceKind_RESOURCE_KIND_SPOT_ORDER:
		err = listAdminOrders(ctx, req, keyword, owner, out)

	default:
		return nil, invalidAdminInput()
	}
	return out, err
}

func adminTimeFilter(q *bun.SelectQuery, alias string, req *adminv1.ListRecordsRequest) *bun.SelectQuery {
	if req.CreatedFrom != nil {
		q = q.Where("?.created_at >= ?", bun.Ident(alias), req.CreatedFrom.AsTime())
	}
	if req.CreatedTo != nil {
		q = q.Where("?.created_at <= ?", bun.Ident(alias), req.CreatedTo.AsTime())
	}
	return q
}

func GetAdminRecord(ctx context.Context, req *adminv1.GetRecordRequest) (*adminv1.GetRecordResponse, error) {
	if _, err := admin.Require(ctx); err != nil {
		return nil, err
	}
	id, err := admin.ParseID(req.Id)
	if err != nil {
		return nil, err
	}
	record, err := getAdminSpotRecord(ctx, postgres.DB, req.Resource, id, false)
	if err != nil {
		return nil, err
	}
	response := &adminv1.GetRecordResponse{Record: record}
	if req.Resource == adminv1.ResourceKind_RESOURCE_KIND_SPOT_ORDER {
		var history []struct {
			Snapshot json.RawMessage `bun:"snapshot"`
		}
		if err := postgres.DB.NewSelect().
			TableExpr("spot.admin_round_history").
			Column("snapshot").
			Where("resource = ? AND resource_id = ?", "spot_order", id).
			OrderExpr("lifecycle_round DESC").
			Limit(100).
			Scan(ctx, &history); err != nil {
			return nil, err
		}
		for _, entry := range history {
			var snapshot spotCorrectionSnapshot
			if err := json.Unmarshal(entry.Snapshot, &snapshot); err != nil {
				return nil, err
			}
			previous, err := adminOrderRecord(ctx, postgres.DB, &snapshot.Order)
			if err != nil {
				return nil, err
			}
			previous.Title = fmt.Sprintf("历史第%d轮 · %s", snapshot.Order.LifecycleRound, previous.Title)
			previous.AllowedActions = nil
			response.RelatedRecords = append(response.RelatedRecords, previous)
		}
	}
	return response, nil
}

func getAdminSpotRecord(
	ctx context.Context,
	db bun.IDB,
	resource adminv1.ResourceKind,
	id int64,
	lock bool,
) (*adminv1.AdminRecord, error) {
	switch resource {
	case adminv1.ResourceKind_RESOURCE_KIND_SPOT_GOODS:
		var item model.SpotGoods
		q := db.NewSelect().Model(&item).Where("id = ?", id)
		if lock {
			q = q.For("UPDATE")
		}
		if err := q.Scan(ctx); err != nil {
			return nil, adminLookupError(err)
		}
		return adminGoodsRecord(&item), nil
	case adminv1.ResourceKind_RESOURCE_KIND_SPOT_ORDER:
		var item model.SpotOrder
		q := db.NewSelect().Model(&item).Where("id = ?", id)
		if lock {
			q = q.For("UPDATE")
		}
		if err := q.Scan(ctx); err != nil {
			return nil, adminLookupError(err)
		}
		return adminOrderRecord(ctx, db, &item)
	default:
		return nil, invalidAdminInput()
	}
}

func MutateAdminRecord(ctx context.Context, req *adminv1.MutateRecordRequest) (*adminv1.MutateRecordResponse, error) {
	actor, err := admin.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := validateSpotAdminMutation(req)
	if err != nil {
		return nil, err
	}
	mutation := admin.Mutation{
		Domain:    "spot",
		ActorID:   actor.UserID,
		Action:    req.Resource.String() + "." + req.Action,
		RequestID: req.RequestId,
		Input:     req,
	}
	var out adminv1.MutateRecordResponse
	err = postgres.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return mutateSpotAdminInTx(ctx, tx, req, id, mutation, &out)
	})
	return &out, err
}

func adminGoodsRecord(item *model.SpotGoods) *adminv1.AdminRecord {
	state := "active"
	if item.StockTotal < 0 {
		state = "delisted"
	}
	if item.ClosedAt != nil {
		state = "closed"
	}
	if item.ModerationStatus == "blocked" {
		state = "blocked"
	}
	action := "block"
	if item.ModerationStatus == "blocked" {
		action = "unblock"
	}
	amount := int64(item.SalePriceCents)
	return &adminv1.AdminRecord{
		Resource:       adminv1.ResourceKind_RESOURCE_KIND_SPOT_GOODS,
		Id:             strconv.FormatInt(item.ID, 10),
		Title:          fmt.Sprintf("现货 #%d", item.ID),
		Status:         state,
		OwnerId:        strconv.FormatInt(item.SellerID, 10),
		CreatedAt:      timestamppb.New(item.CreatedAt),
		UpdatedAt:      timestamppb.New(item.UpdatedAt),
		LifecycleRound: 1,
		AmountCents:    &amount,
		AllowedActions: []string{action},
		Fields: []*adminv1.RecordField{
			spotField(
				"seller_id",
				"卖家",
				strconv.FormatInt(item.SellerID, 10),
			),
			spotField("store_id", "店铺", strconv.FormatInt(item.StoreID, 10)),
			spotField("product_template_id", "商品模板", strconv.FormatInt(item.ProductTemplateID, 10)),
			spotField("sale_price_cents", "售价（分）", strconv.FormatInt(amount, 10)),
			spotField("stock_total", "库存", strconv.FormatInt(int64(item.StockTotal), 10)),
			spotField("closed_at", "关闭时间", adminTime(item.ClosedAt)),
			spotField("moderation_status", "治理状态", item.ModerationStatus),
			spotField("moderation_reason", "治理原因", item.ModerationReason),
		},
	}
}

func adminOrderRecord(ctx context.Context, db bun.IDB, item *model.SpotOrder) (*adminv1.AdminRecord, error) {
	var goods model.SpotGoods
	if err := db.NewSelect().Model(&goods).Where("id = ?", item.ListingID).Scan(ctx); err != nil {
		return nil, err
	}
	amount := int64(item.TotalAmountCents)
	return &adminv1.AdminRecord{
		Resource:       adminv1.ResourceKind_RESOURCE_KIND_SPOT_ORDER,
		Id:             strconv.FormatInt(item.ID, 10),
		Title:          item.OrderNo + " · " + item.TitleSnapshot,
		Status:         string(item.Status),
		OwnerId:        strconv.FormatInt(item.PurchaserID, 10),
		CreatedAt:      timestamppb.New(item.CreatedAt),
		UpdatedAt:      timestamppb.New(item.UpdatedAt),
		LifecycleRound: max(item.LifecycleRound, 1),
		AmountCents:    &amount,
		AllowedActions: spotAdminOrderActions(item),
		Fields: []*adminv1.RecordField{
			spotField(
				"order_no",
				"订单号",
				item.OrderNo,
			),
			spotField("inventory_state", "库存扣减状态", item.InventoryState),
			spotField("admin_operation_id", "正在处理的操作", item.AdminOperationID),
			spotField("purchaser_id", "买家", strconv.FormatInt(item.PurchaserID, 10)),
			spotField("seller_id", "卖家", strconv.FormatInt(goods.SellerID, 10)),
			spotField("listing_id", "现货编号", strconv.FormatInt(item.ListingID, 10)),
			spotField("product_template_id", "商品模板", strconv.FormatInt(item.ProductTemplateID, 10)),
			spotField("quantity", "数量", strconv.FormatInt(int64(item.Quantity), 10)),
			spotField("unit_price_cents", "单价（分）", strconv.FormatInt(int64(item.UnitPriceCents), 10)),
			spotField("total_amount_cents", "总额（分）", strconv.FormatInt(amount, 10)),
			spotField("payment_bill_id", "账单编号", strconv.FormatInt(derefInt64(item.PaymentBillID), 10)),
			spotField("paid_at", "付款时间", adminTime(item.PaidAt)),
			spotField("completed_at", "完成时间", adminTime(item.CompletedAt)),
			spotField("cancelled_at", "取消时间", adminTime(item.CancelledAt)),
		},
	}, nil
}

func spotField(key, label, value string) *adminv1.RecordField {
	return &adminv1.RecordField{Key: key, Label: label, Value: value}
}

func adminTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func invalidAdminInput() error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New("管理操作参数不正确"))
}

func adminLookupError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return connect.NewError(connect.CodeNotFound, errors.New("记录不存在"))
	}
	return err
}

func GetAdminStats(ctx context.Context) (*adminv1.GetStatsResponse, error) {
	if _, err := admin.Require(ctx); err != nil {
		return nil, err
	}
	goods, err := postgres.DB.NewSelect().Model((*model.SpotGoods)(nil)).Count(ctx)
	if err != nil {
		return nil, err
	}
	orders, err := postgres.DB.NewSelect().Model((*model.SpotOrder)(nil)).Count(ctx)
	if err != nil {
		return nil, err
	}
	blocked, err := postgres.DB.NewSelect().
		Model((*model.SpotGoods)(nil)).
		Where("moderation_status = ?", "blocked").
		Count(ctx)
	if err != nil {
		return nil, err
	}
	return &adminv1.GetStatsResponse{
		Metrics: []*adminv1.Metric{
			{Key: "spot_goods", Label: "现货商品", Value: int64(goods)},
			{Key: "spot_orders", Label: "现货订单", Value: int64(orders)},
			{Key: "blocked_spot_goods", Label: "管理员下架", Value: int64(blocked)},
		},
	}, nil
}

func adminAuditSnapshot(record *adminv1.AdminRecord) map[string]string {
	if record == nil {
		return nil
	}
	snapshot := map[string]string{
		"id":              record.Id,
		"title":           record.Title,
		"status":          record.Status,
		"owner_id":        record.OwnerId,
		"lifecycle_round": strconv.FormatInt(record.LifecycleRound, 10),
		"updated_at":      record.UpdatedAt.AsTime().UTC().Format(time.RFC3339Nano),
	}
	for _, field := range record.Fields {
		snapshot[field.Key] = field.Value
	}
	return snapshot
}

func listAdminGoods(
	ctx context.Context,
	req *adminv1.ListRecordsRequest,
	keyword string,
	owner int64,
	out *adminv1.ListRecordsResponse,
) error {
	page, size := out.Page, out.PageSize
	var items []*model.SpotGoods
	query := postgres.DB.NewSelect().Model(&items)
	if keyword != "" {
		query = query.Where(
			"(sg.id::text = ? OR EXISTS (SELECT 1 FROM catalog.catalog_product_template cpt WHERE cpt.id=sg.product_template_id AND strpos(lower(cpt.title),lower(?))>0))",
			keyword,
			keyword,
		)
	}
	if owner > 0 {
		query = query.Where("sg.seller_id = ?", owner)
	}
	switch req.Status {
	case "", "allowed", "blocked":
		if req.Status != "" {
			query = query.Where("sg.moderation_status = ?", req.Status)
		}
	case "closed":
		query = query.Where("sg.closed_at IS NOT NULL")
	case "delisted":
		query = query.Where("sg.stock_total < 0").
			Where("sg.closed_at IS NULL").
			Where("sg.moderation_status = 'allowed'")
	case "active":
		query = query.Where("sg.stock_total >= 0").
			Where("sg.closed_at IS NULL").
			Where("sg.moderation_status = 'allowed'")
	default:
		return invalidAdminInput()
	}
	query = adminTimeFilter(query, "sg", req)
	count, err := query.OrderExpr("sg.id DESC").Limit(int(size)).Offset(int(page-1) * int(size)).ScanAndCount(ctx)
	if err != nil {
		return err
	}
	out.TotalCount = int64(count)
	for _, item := range items {
		out.Records = append(out.Records, adminGoodsRecord(item))
	}

	return nil
}

func listAdminOrders(
	ctx context.Context,
	req *adminv1.ListRecordsRequest,
	keyword string,
	owner int64,
	out *adminv1.ListRecordsResponse,
) error {
	page, size := out.Page, out.PageSize
	var items []*model.SpotOrder
	query := postgres.DB.NewSelect().Model(&items)
	if keyword != "" {
		query = query.Where(
			"(so.id::text = ? OR strpos(lower(so.order_no),lower(?))>0 OR strpos(lower(so.title_snapshot),lower(?))>0)",
			keyword,
			keyword,
			keyword,
		)
	}
	if owner > 0 {
		query = query.Where(
			"(so.purchaser_id = ? OR EXISTS (SELECT 1 FROM spot.spot_goods sg WHERE sg.id=so.listing_id AND sg.seller_id=?))",
			owner,
			owner,
		)
	}
	if req.Status != "" {
		switch req.Status {
		case "pending_payment", "paid", "completed", "cancelled":
			query = query.Where("so.status = ?", req.Status)
		default:
			return invalidAdminInput()
		}
	}
	query = adminTimeFilter(query, "so", req)
	count, err := query.OrderExpr("so.id DESC").Limit(int(size)).Offset(int(page-1) * int(size)).ScanAndCount(ctx)
	if err != nil {
		return err
	}
	out.TotalCount = int64(count)
	for _, item := range items {
		record, err := adminOrderRecord(ctx, postgres.DB, item)
		if err != nil {
			return err
		}
		out.Records = append(out.Records, record)
	}
	return nil
}

func validateSpotAdminMutation(req *adminv1.MutateRecordRequest) (int64, error) {
	if req == nil || len(req.Values) != 0 || !validSpotAdminAction(req) {
		return 0, invalidAdminInput()
	}
	id, err := admin.ParseID(req.Id)
	if err != nil {
		return 0, err
	}
	if req.ExpectedUpdatedAt == nil || !req.ExpectedUpdatedAt.IsValid() || req.ExpectedRound < 1 {
		return 0, invalidAdminInput()
	}
	if err := admin.ValidateReason(req.Reason, req.Evidence); err != nil {
		return 0, err
	}

	return id, nil
}

func validSpotAdminAction(req *adminv1.MutateRecordRequest) bool {
	if req.Resource == adminv1.ResourceKind_RESOURCE_KIND_SPOT_ORDER {
		return req.Action == "complete" && strings.TrimSpace(req.Evidence) != ""
	}
	return req.Resource == adminv1.ResourceKind_RESOURCE_KIND_SPOT_GOODS && req.ExpectedRound == 1 &&
		(req.Action == "block" || req.Action == "unblock")
}

func mutateSpotAdminInTx(
	ctx context.Context,
	tx bun.Tx,
	req *adminv1.MutateRecordRequest,
	id int64,
	mutation admin.Mutation,
	out *adminv1.MutateRecordResponse,
) error {
	if _, err := admin.RequireCurrent(ctx, tx, false); err != nil {
		return err
	}
	result, err := admin.BeginMutation(ctx, tx, mutation)
	if err != nil {
		return err
	}
	if result.Replay {
		return json.Unmarshal(result.Response, out)
	}
	before, err := getAdminSpotRecord(ctx, tx, req.Resource, id, true)
	if err != nil {
		return err
	}
	if !before.UpdatedAt.AsTime().Equal(req.ExpectedUpdatedAt.AsTime()) || before.LifecycleRound != req.ExpectedRound {
		return connect.NewError(connect.CodeAborted, errors.New("记录已变更，请刷新后重试"))
	}
	if err := applySpotAdminMutation(ctx, tx, req, id, mutation.ActorID); err != nil {
		return err
	}
	after, err := getAdminSpotRecord(ctx, tx, req.Resource, id, false)
	if err != nil {
		return err
	}
	if err := admin.WriteAudit(
		ctx,
		tx,
		admin.Entry{
			Domain:     "spot",
			ActorID:    mutation.ActorID,
			Action:     req.Action,
			TargetType: strings.TrimPrefix(strings.ToLower(req.Resource.String()), "resource_kind_"),
			TargetID:   id,
			RequestID:  req.RequestId,
			Reason:     req.Reason,
			Evidence:   req.Evidence,
			Before:     adminAuditSnapshot(before),
			After:      adminAuditSnapshot(after),
		},
	); err != nil {
		return err
	}
	out.Record = after
	return admin.CompleteMutation(ctx, tx, mutation, out)
}

func applySpotAdminMutation(ctx context.Context, tx bun.Tx, req *adminv1.MutateRecordRequest, id, actorID int64) error {
	if req.Resource == adminv1.ResourceKind_RESOURCE_KIND_SPOT_ORDER {
		return completeAdminSpotOrder(ctx, tx, id)
	}
	status := "blocked"
	if req.Action == "unblock" {
		status = "allowed"
	}
	_, err := tx.NewUpdate().Model((*model.SpotGoods)(nil)).
		Set("moderation_status = ?", status).
		Set("moderation_reason = ?", strings.TrimSpace(req.Reason)).
		Set("moderation_by = ?", actorID).
		Set("moderation_at = ?", time.Now()).
		Set("updated_at = ?", time.Now()).
		Where("id = ?", id).Exec(ctx)
	return err
}
