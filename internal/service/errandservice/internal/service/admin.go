package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/adminflow"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type adminErrandRecord struct {
	ID                   int64
	OwnerID              int64
	StoreID              int64
	Title                string
	Status               string
	TaskID               *int64
	Deadline             *time.Time
	PackagingFeeCents    int32
	LifecycleRound       int64
	AdminOperationID     string
	CreatedAt, UpdatedAt time.Time
}

func errandAdminQuery(db bun.IDB, kind av.ResourceKind) (*bun.SelectQuery, error) {
	switch kind {
	case av.ResourceKind_RESOURCE_KIND_ERRAND_TASK:
		return db.NewSelect().
				TableExpr("errand.errand_task").
				ColumnExpr("id,captain_id AS owner_id,store_id,task_no AS title,status,packaging_fee_cents,lifecycle_round,admin_operation_id,created_at,updated_at"),
			nil
	case av.ResourceKind_RESOURCE_KIND_ERRAND_DEMAND:
		return db.NewSelect().
				TableExpr("errand.errand_demand").
				ColumnExpr("id,requester_id AS owner_id,store_id,'需求 ' || id::text AS title,status,task_id,deadline,lifecycle_round,admin_operation_id,created_at,updated_at"),
			nil
	default:
		return nil, adminflow.Failure(connect.CodeInvalidArgument, "仅支持跑腿需求与采购任务")
	}
}

func errandAdminProto(row *adminErrandRecord, kind av.ResourceKind) *av.AdminRecord {
	var actions []string
	if row.AdminOperationID == "" {
		if row.Status == "cancelled" {
			actions = append(actions, "reopen")
		} else {
			actions = append(actions, "cancel")
		}
		if row.Status == "completed" && kind == av.ResourceKind_RESOURCE_KIND_ERRAND_TASK {
			actions = append(actions, "reverse_completion")
		}
	}
	if kind == av.ResourceKind_RESOURCE_KIND_ERRAND_DEMAND && row.TaskID != nil {
		actions = nil
	}
	record := &av.AdminRecord{
		Resource:       kind,
		Id:             fmt.Sprint(row.ID),
		Title:          row.Title,
		Status:         row.Status,
		OwnerId:        fmt.Sprint(row.OwnerID),
		LifecycleRound: row.LifecycleRound,
		CreatedAt:      timestamppb.New(row.CreatedAt),
		UpdatedAt:      timestamppb.New(row.UpdatedAt),
		AllowedActions: actions,
		Fields: []*av.RecordField{
			{Key: "store_id", Label: "店铺编号", Value: fmt.Sprint(row.StoreID)},
			{Key: "admin_operation_id", Label: "处理中操作", Value: row.AdminOperationID},
		},
	}
	if kind == av.ResourceKind_RESOURCE_KIND_ERRAND_TASK {
		record.Fields = append(
			record.Fields,
			&av.RecordField{Key: "packaging_fee_cents", Label: "包装费（分）", Value: fmt.Sprint(row.PackagingFeeCents)},
		)
	}
	if row.TaskID != nil {
		record.Fields = append(
			record.Fields,
			&av.RecordField{Key: "task_id", Label: "关联任务", Value: fmt.Sprint(*row.TaskID)},
		)
	}
	if row.Deadline != nil {
		record.Fields = append(
			record.Fields,
			&av.RecordField{Key: "deadline", Label: "截止时间", Value: row.Deadline.UTC().Format(time.RFC3339)},
		)
	}
	return record
}

func readAdminErrand(
	ctx context.Context,
	db bun.IDB,
	kind av.ResourceKind,
	id int64,
	lock bool,
) (*adminErrandRecord, error) {
	q, e := errandAdminQuery(db, kind)
	if e != nil {
		return nil, e
	}
	q = q.Where("id=?", id)
	if lock {
		q = q.For("UPDATE")
	}
	row := new(adminErrandRecord)
	e = q.Scan(ctx, row)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, adminflow.Failure(connect.CodeNotFound, "跑腿记录不存在")
	}
	if e != nil {
		return nil, e
	}
	return row, nil
}

//nolint:gocyclo // Apply optional filters uniformly to the two supported projections.
func AdminErrandList(ctx context.Context, r *av.ListRecordsRequest) (*av.ListRecordsResponse, error) {
	if _, e := admin.Require(ctx); e != nil {
		return nil, e
	}
	page, size, e := admin.Page(r.Page, r.PageSize)
	if e != nil {
		return nil, e
	}
	if e = admin.TimeRange(r.CreatedFrom, r.CreatedTo); e != nil {
		return nil, e
	}
	if !utf8.ValidString(r.Keyword) || strings.ContainsRune(r.Keyword, '\x00') ||
		utf8.RuneCountInString(r.Keyword) > 100 {
		return nil, adminflow.Failure(connect.CodeInvalidArgument, "搜索关键词无效")
	}
	q, e := errandAdminQuery(postgres.DB, r.Resource)
	if e != nil {
		return nil, e
	}
	if r.Keyword != "" {
		if r.Resource == av.ResourceKind_RESOURCE_KIND_ERRAND_TASK {
			q = q.Where("strpos(lower(task_no),lower(?))>0 OR id::text=?", r.Keyword, r.Keyword)
		} else {
			q = q.Where("id::text=?", r.Keyword)
		}
	}
	if r.UserId != "" {
		id, e := admin.ParseID(r.UserId)
		if e != nil {
			return nil, e
		}
		column := "requester_id"
		if r.Resource == av.ResourceKind_RESOURCE_KIND_ERRAND_TASK {
			column = "captain_id"
		}
		q = q.Where("?=?", bun.Ident(column), id)
	}
	if r.Status != "" {
		q = q.Where("status::text=?", r.Status)
	}
	if r.CreatedFrom != nil {
		q = q.Where("created_at>=?", r.CreatedFrom.AsTime())
	}
	if r.CreatedTo != nil {
		q = q.Where("created_at<=?", r.CreatedTo.AsTime())
	}
	var rows []adminErrandRecord
	count, e := q.OrderExpr("created_at DESC,id DESC").
		Limit(int(size)).
		Offset(int((page-1)*size)).
		ScanAndCount(ctx, &rows)
	if e != nil {
		return nil, e
	}
	out := &av.ListRecordsResponse{TotalCount: int64(count), Page: page, PageSize: size}
	for i := range rows {
		out.Records = append(out.Records, errandAdminProto(&rows[i], r.Resource))
	}
	return out, nil
}

func AdminErrandGet(ctx context.Context, r *av.GetRecordRequest) (*av.GetRecordResponse, error) {
	if _, e := admin.Require(ctx); e != nil {
		return nil, e
	}
	id, e := admin.ParseID(r.Id)
	if e != nil {
		return nil, e
	}
	row, e := readAdminErrand(ctx, postgres.DB, r.Resource, id, false)
	if e != nil {
		return nil, e
	}
	out := &av.GetRecordResponse{Record: errandAdminProto(row, r.Resource)}
	if r.Resource == av.ResourceKind_RESOURCE_KIND_ERRAND_DEMAND && row.TaskID != nil {
		task, e := readAdminErrand(ctx, postgres.DB, av.ResourceKind_RESOURCE_KIND_ERRAND_TASK, *row.TaskID, false)
		if e != nil {
			return nil, e
		}
		out.RelatedRecords = append(
			out.RelatedRecords,
			errandAdminProto(task, av.ResourceKind_RESOURCE_KIND_ERRAND_TASK),
		)
	}
	if r.Resource == av.ResourceKind_RESOURCE_KIND_ERRAND_TASK {
		var demands []adminErrandRecord
		q, e := errandAdminQuery(postgres.DB, av.ResourceKind_RESOURCE_KIND_ERRAND_DEMAND)
		if e != nil {
			return nil, e
		}
		if e = q.Where("task_id=?", id).Order("id").Limit(200).Scan(ctx, &demands); e != nil {
			return nil, e
		}
		for i := range demands {
			out.RelatedRecords = append(
				out.RelatedRecords,
				errandAdminProto(&demands[i], av.ResourceKind_RESOURCE_KIND_ERRAND_DEMAND),
			)
		}
		var item struct{ Items, Assignments int64 }
		if e = postgres.DB.NewRaw(`SELECT (SELECT count(*) FROM errand.errand_task_item WHERE task_id=?) AS items,(SELECT count(*) FROM errand.errand_task_assignment WHERE task_id=?) AS assignments`, id, id).
			Scan(ctx, &item); e != nil {
			return nil, e
		}
		out.Record.Fields = append(
			out.Record.Fields,
			&av.RecordField{Key: "items", Label: "采购商品行", Value: fmt.Sprint(item.Items)},
			&av.RecordField{Key: "assignments", Label: "分发记录数", Value: fmt.Sprint(item.Assignments)},
		)
	}
	var details []struct{ key, label, query string }
	if r.Resource == av.ResourceKind_RESOURCE_KIND_ERRAND_TASK {
		details = []struct{ key, label, query string }{
			{
				"task_items_json",
				"采购行明细",
				"SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id::text,'product_template_id',product_template_id::text,'title',title_snapshot,'required_quantity',required_quantity,'purchased_quantity',purchased_quantity,'actual_unit_price_cents',actual_unit_price_cents,'deadline',deadline) ORDER BY id),'[]'::jsonb) FROM errand.errand_task_item WHERE task_id=?",
			},
			{
				"assignments_json",
				"原分配明细",
				"SELECT COALESCE(jsonb_agg(jsonb_build_object('task_item_id',task_item_id::text,'demand_item_id',demand_item_id::text,'purchaser_id',purchaser_id::text,'distributed_quantity',distributed_quantity,'service_fee_per_unit_cents',service_fee_per_unit_cents,'payment_bill_id',payment_bill_id::text) ORDER BY id),'[]'::jsonb) FROM errand.errand_task_assignment WHERE task_id=?",
			},
		}
	} else {
		details = []struct{ key, label, query string }{
			{
				"demand_items_json",
				"需求行明细",
				"SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id::text,'product_template_id',product_template_id::text,'requester_id',requester_id::text,'quantity',quantity,'service_fee_per_unit_cents',service_fee_per_unit_cents,'status',status) ORDER BY id),'[]'::jsonb) FROM errand.errand_demand_item WHERE errand_demand_id=?",
			},
		}
	}
	for _, detail := range details {
		var value string
		if e = postgres.DB.NewRaw(detail.query, id).Scan(ctx, &value); e != nil {
			return nil, e
		}
		out.Record.Fields = append(
			out.Record.Fields,
			&av.RecordField{Key: detail.key, Label: detail.label, Value: value},
		)
	}
	return out, nil
}

func AdminErrandStats(ctx context.Context) (*av.GetStatsResponse, error) {
	if _, e := admin.Require(ctx); e != nil {
		return nil, e
	}
	var v struct{ Demands, Tasks, Shopping, Collecting int64 }
	e := postgres.DB.NewRaw(`SELECT (SELECT count(*) FROM errand.errand_demand) AS demands,(SELECT count(*) FROM errand.errand_task) AS tasks,(SELECT count(*) FROM errand.errand_task WHERE status='shopping') AS shopping,(SELECT count(*) FROM errand.errand_task WHERE status='collecting_payment') AS collecting`).
		Scan(ctx, &v)
	if e != nil {
		return nil, e
	}
	return &av.GetStatsResponse{
		Metrics: []*av.Metric{
			{Key: "errand_demands", Label: "需求总数", Value: v.Demands},
			{Key: "errand_tasks", Label: "任务总数", Value: v.Tasks},
			{Key: "shopping_tasks", Label: "采购中任务", Value: v.Shopping},
			{Key: "collecting_tasks", Label: "收款中任务", Value: v.Collecting},
		},
	}, nil
}
