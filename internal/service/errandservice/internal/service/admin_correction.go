package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/adminflow"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/repository"
	"github.com/uptrace/bun"
)

type adminDemandSnapshot struct {
	Record adminErrandRecord
	Data   json.RawMessage
}
type adminErrandSnapshot struct {
	Record              adminErrandRecord
	Data                json.RawMessage
	Items               []*model.ErrandTaskItem
	ExistingAssignments []*model.ErrandTaskAssignment
	Assignments         []*model.ErrandTaskAssignment
	Demands             []adminDemandSnapshot
	DemandItems         []*model.ErrandDemandItem
	Bills               []*av.SourceBill
	RestorationBills    []*av.SourceBill
	TargetStatus        string
	Deadline            *time.Time
}
type restoreAssignment struct {
	TaskItemID          string `json:"task_item_id"`
	DemandItemID        string `json:"demand_item_id"`
	DistributedQuantity *int32 `json:"distributed_quantity"`
}

func AdminErrandEngine() *adminflow.Engine {
	return &adminflow.Engine{
		DB:      postgres.DB,
		Domain:  "errand",
		Build:   buildErrandCorrection,
		Reserve: reserveErrandCorrection,
		Apply:   applyErrandCorrection,
	}
}

func errandFailure(message string) error {
	return adminflow.Failure(connect.CodeFailedPrecondition, message)
}

//nolint:gocyclo // Validate the complete dependency snapshot before exposing a correction preview.
func buildErrandCorrection(ctx context.Context, r *av.PreviewCorrectionRequest) (*adminflow.Plan, error) {
	id, e := admin.ParseID(r.Id)
	if e != nil {
		return nil, e
	}
	for key := range r.Values {
		switch key {
		case "funds_handling",
			"target_status",
			"restore_assignments",
			"deadline",
			"missing_bill_confirmed",
			"submitted_unreceived_confirmed":
		default:
			return nil, adminflow.Failure(connect.CodeInvalidArgument, "不支持的跑腿恢复字段")
		}
	}
	row, e := readAdminErrand(ctx, postgres.DB, r.Resource, id, false)
	if e != nil {
		return nil, e
	}
	if row.AdminOperationID != "" || row.LifecycleRound != r.ExpectedRound ||
		!row.UpdatedAt.Equal(r.ExpectedUpdatedAt.AsTime()) {
		return nil, adminflow.Failure(connect.CodeAborted, "记录已经变化，请重新预览")
	}
	snap := adminErrandSnapshot{Record: *row}
	table := "errand_demand"
	if r.Resource == av.ResourceKind_RESOURCE_KIND_ERRAND_TASK {
		table = "errand_task"
	}
	var sourceData string
	if e = postgres.DB.NewRaw("SELECT to_jsonb(t) FROM ? AS t WHERE id=?", bun.Ident("errand."+table), id).
		Scan(ctx, &sourceData); e != nil {
		return nil, e
	}
	snap.Data = json.RawMessage(sourceData)
	if raw := r.Values["deadline"]; raw != "" {
		deadline, e := time.Parse(time.RFC3339, raw)
		if e != nil || !deadline.After(time.Now()) {
			return nil, adminflow.Failure(connect.CodeInvalidArgument, "恢复截止时间必须为未来的RFC3339时间")
		}
		snap.Deadline = &deadline
	}
	if r.Resource == av.ResourceKind_RESOURCE_KIND_ERRAND_DEMAND {
		return buildStandaloneDemandCorrection(ctx, r, &snap)
	}
	if e = postgres.DB.NewSelect().Model(&snap.Items).Where("task_id=?", id).Order("id").Scan(ctx); e != nil {
		return nil, e
	}
	if e = postgres.DB.NewSelect().
		Model(&snap.ExistingAssignments).
		Where("task_id=?", id).
		Order("id").
		Scan(ctx); e != nil {
		return nil, e
	}
	snap.Assignments = snap.ExistingAssignments
	snap.Bills, e = adminflow.GetSourceBills(ctx, "errand_task", id, row.LifecycleRound)
	if e != nil {
		return nil, e
	}
	switch r.Action {
	case "cancel":
		if row.Status == "cancelled" {
			return nil, errandFailure("任务已经取消")
		}
		snap.TargetStatus = "cancelled"
	case "reverse_completion":
		if row.Status != "completed" {
			return nil, errandFailure("只能撤销已完成任务")
		}
		if funds := r.Values["funds_handling"]; funds != "refunded" && funds != "confirmation_error" {
			return nil, adminflow.Failure(connect.CodeInvalidArgument, "撤销已完成任务必须选择已核验退款或原收款确认有误；保留全部收款会自动完成任务")
		}
		snap.TargetStatus = "collecting_payment"
	case "reopen":
		if row.Status != "cancelled" {
			return nil, errandFailure("只能恢复已取消任务")
		}
		snap.TargetStatus = r.Values["target_status"]
		if snap.TargetStatus == "" {
			snap.TargetStatus = "shopping"
			if len(snap.Bills) > 0 {
				snap.TargetStatus = "collecting_payment"
			}
		}
		if snap.TargetStatus != "shopping" && snap.TargetStatus != "collecting_payment" {
			return nil, adminflow.Failure(connect.CodeInvalidArgument, "恢复状态仅支持shopping或collecting_payment")
		}
		if snap.TargetStatus == "shopping" && len(snap.Bills) > 0 {
			return nil, errandFailure("已有账单的任务须恢复到收款阶段，避免旧金额在采购中生效")
		}
	default:
		return nil, adminflow.Failure(connect.CodeInvalidArgument, "不支持的跑腿纠错动作")
	}
	if len(snap.Assignments) == 0 && r.Action != "cancel" {
		var data string
		e = postgres.DB.NewRaw(`SELECT snapshot FROM errand.admin_round_history WHERE resource='errand_task' AND resource_id=? ORDER BY lifecycle_round DESC LIMIT 1`, id).
			Scan(ctx, &data)
		if e == nil {
			var history adminErrandSnapshot
			if e = json.Unmarshal([]byte(data), &history); e != nil {
				return nil, e
			}
			snap.Assignments = history.Assignments
		} else if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
	}
	if r.Values["restore_assignments"] != "" {
		if len(snap.ExistingAssignments) > 0 {
			return nil, errandFailure("现有分配记录完整，不接受覆盖式补录")
		}
		snap.Assignments, e = parseErrandRestoration(ctx, id, r.Values["restore_assignments"])
		if e != nil {
			return nil, e
		}
	}
	if len(snap.Assignments) == 0 {
		return nil, errandFailure("历史分配记录缺失，请补录restore_assignments并核验需求归属")
	}
	if e = loadErrandDemands(ctx, &snap); e != nil {
		return nil, e
	}
	if e = validateErrandRestoration(ctx, &snap, r); e != nil {
		return nil, e
	}
	return errandPlan(r, &snap)
}

func buildStandaloneDemandCorrection(
	ctx context.Context,
	r *av.PreviewCorrectionRequest,
	snap *adminErrandSnapshot,
) (*adminflow.Plan, error) {
	if snap.Record.TaskID != nil {
		return nil, errandFailure("此需求已归属采购任务，请对关联任务进行整单纠错")
	}
	switch r.Action {
	case "cancel":
		if snap.Record.Status != "open" {
			return nil, errandFailure("仅未接单需求可独立取消")
		}
		snap.TargetStatus = "cancelled"
	case "reopen":
		if snap.Record.Status != "cancelled" {
			return nil, errandFailure("仅已取消需求可恢复")
		}
		snap.TargetStatus = "open"
	default:
		return nil, adminflow.Failure(connect.CodeInvalidArgument, "需求仅支持取消和恢复")
	}
	if e := postgres.DB.NewSelect().
		Model(&snap.DemandItems).
		Where("errand_demand_id=?", snap.Record.ID).
		Order("id").
		Scan(ctx); e != nil {
		return nil, e
	}
	if len(snap.DemandItems) == 0 {
		return nil, errandFailure("需求行已丢失，不能恢复")
	}
	if snap.TargetStatus == "open" && snap.Deadline == nil &&
		(snap.Record.Deadline == nil || !snap.Record.Deadline.After(time.Now())) {
		return nil, errandFailure("原截止时间已过，请填写新的deadline")
	}
	return errandPlan(r, snap)
}

func errandPlan(r *av.PreviewCorrectionRequest, snap *adminErrandSnapshot) (*adminflow.Plan, error) {
	data, e := json.Marshal(snap)
	if e != nil {
		return nil, e
	}
	steps := []adminflow.Step{}
	if r.Resource == av.ResourceKind_RESOURCE_KIND_ERRAND_TASK {
		steps = append(steps, adminflow.Step{Key: "payment", Description: "核验和纠正整组跑腿账单", Status: "pending"})
	}
	steps = append(steps, adminflow.Step{Key: "errand", Description: "恢复原编号及全部关联需求和分配", Status: "pending"})
	return &adminflow.Plan{
		Record:   errandAdminProto(&snap.Record, r.Resource),
		Snapshot: data,
		Steps:    steps,
		Impacts: []*av.Impact{
			{
				Resource:    r.Resource,
				ResourceId:  r.Id,
				Description: "原编号保留，生成完整旧轮次快照",
				Before:      fmt.Sprintf("%s / 第%d轮", snap.Record.Status, snap.Record.LifecycleRound),
				After:       fmt.Sprintf("%s / 第%d轮", snap.TargetStatus, snap.Record.LifecycleRound+1),
			},
			{
				Resource:    av.ResourceKind_RESOURCE_KIND_ERRAND_DEMAND,
				ResourceId:  r.Id,
				Description: "全部关联需求、采购行、分配同步调整",
				Before:      fmt.Sprintf("%d 个需求 / %d 个分配", len(snap.Demands), len(snap.Assignments)),
				After:       snap.TargetStatus,
			},
		},
		Warnings: []string{
			"不会自动退款，资金处理必须与核实依据一致", "申报付款须逐笔核实；实际已到账应先确认收款再整单纠错", "已采购或交付的实物不会自动撤回，须独立核验履约情况",
			"取消整单会同时取消关联需求，保留采购与分发历史",
			"恢复不会抢占已经归属其他采购任务的需求",
			"撤销完成仅支持已核验退款或原收款确认有误，保留收款会按实际支付状态完成任务",
		},
	}, nil
}

func parseErrandRestoration(ctx context.Context, taskID int64, raw string) ([]*model.ErrandTaskAssignment, error) {
	var inputs []restoreAssignment
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&inputs); e != nil || len(inputs) == 0 || len(inputs) > 200 {
		return nil, adminflow.Failure(connect.CodeInvalidArgument, "restore_assignments必须为不超过200项的明确分配数组")
	}
	out := make([]*model.ErrandTaskAssignment, 0, len(inputs))
	for _, input := range inputs {
		taskItem, e := admin.ParseID(input.TaskItemID)
		if e != nil {
			return nil, e
		}
		demandItem, e := admin.ParseID(input.DemandItemID)
		if e != nil {
			return nil, e
		}
		var item model.ErrandDemandItem
		if e = postgres.DB.NewSelect().Model(&item).Where("id=?", demandItem).Scan(ctx); e != nil {
			return nil, errandFailure("补录需求行不存在")
		}
		out = append(
			out,
			&model.ErrandTaskAssignment{
				TaskID:                 taskID,
				TaskItemID:             taskItem,
				DemandItemID:           demandItem,
				PurchaserID:            item.RequesterID,
				DistributedQuantity:    input.DistributedQuantity,
				ServiceFeePerUnitCents: item.ServiceFeePerUnitCents,
			},
		)
	}
	return out, nil
}

func loadErrandDemands(ctx context.Context, snap *adminErrandSnapshot) error {
	ids := make([]int64, 0, len(snap.Assignments))
	for _, a := range snap.Assignments {
		ids = append(ids, a.DemandItemID)
	}
	if e := postgres.DB.NewSelect().
		Model(&snap.DemandItems).
		Where("id IN (?)", bun.List(ids)).
		Order("id").
		Scan(ctx); e != nil {
		return e
	}
	demandIDs := map[int64]bool{}
	for _, i := range snap.DemandItems {
		demandIDs[i.ErrandDemandID] = true
	}
	sorted := make([]int64, 0, len(demandIDs))
	for id := range demandIDs {
		sorted = append(sorted, id)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for _, id := range sorted {
		row, e := readAdminErrand(ctx, postgres.DB, av.ResourceKind_RESOURCE_KIND_ERRAND_DEMAND, id, false)
		if e != nil {
			return e
		}
		var data string
		if e = postgres.DB.NewRaw("SELECT to_jsonb(d) FROM errand.errand_demand d WHERE id=?", id).
			Scan(ctx, &data); e != nil {
			return e
		}
		snap.Demands = append(snap.Demands, adminDemandSnapshot{Record: *row, Data: json.RawMessage(data)})
	}
	// Include every row of each parent demand so a partial graph cannot mutate unrelated items.
	if len(sorted) > 0 {
		snap.DemandItems = nil
		if e := postgres.DB.NewSelect().
			Model(&snap.DemandItems).
			Where("errand_demand_id IN (?)", bun.List(sorted)).
			Order("id").
			Scan(ctx); e != nil {
			return e
		}
	}
	return nil
}

//nolint:gocyclo // Recovery validates the complete assignment and amount graph, not individual rows in isolation.
func validateErrandRestoration(ctx context.Context, snap *adminErrandSnapshot, r *av.PreviewCorrectionRequest) error {
	if e := admin.ValidateSourceFunds(
		r.Action,
		r.Values["funds_handling"],
		snap.Bills,
		r.Values["submitted_unreceived_confirmed"] == "true",
	); e != nil {
		return e
	}
	items := map[int64]*model.ErrandTaskItem{}
	for _, i := range snap.Items {
		items[i.ID] = i
	}
	demands := map[int64]*model.ErrandDemandItem{}
	for _, i := range snap.DemandItems {
		demands[i.ID] = i
	}
	seen := map[int64]bool{}
	distributed := map[int64]int64{}
	required := map[int64]int64{}
	amounts := map[int64]int64{}
	var billRows []repository.TaskPaymentBillAssignmentRow
	for _, d := range snap.Demands {
		if d.Record.AdminOperationID != "" || d.Record.TaskID != nil && *d.Record.TaskID != snap.Record.ID {
			return errandFailure("关联需求已被其他任务使用或正在纠错")
		}
		if snap.TargetStatus == "shopping" && snap.Deadline == nil &&
			(d.Record.Deadline == nil || !d.Record.Deadline.After(time.Now())) {
			return errandFailure("原截止时间已过，请补录deadline")
		}
	}
	for _, a := range snap.Assignments {
		i, ok := items[a.TaskItemID]
		if !ok || i.TaskID != snap.Record.ID {
			return errandFailure("补录采购行不属于该任务")
		}
		d, ok := demands[a.DemandItemID]
		if !ok || seen[d.ID] || d.RequesterID != a.PurchaserID || d.ProductTemplateID != i.ProductTemplateID ||
			d.StoreID != snap.Record.StoreID ||
			a.ServiceFeePerUnitCents != d.ServiceFeePerUnitCents || a.ServiceFeePerUnitCents < 0 {
			return errandFailure("分配归属、商品或费用不一致")
		}
		seen[d.ID] = true
		required[i.ID] += int64(d.Quantity)
		count, e := postgres.DB.NewSelect().
			TableExpr("errand.errand_task_assignment").
			Where("demand_item_id=? AND task_id<>?", d.ID, snap.Record.ID).
			Count(ctx)
		if e != nil {
			return e
		}
		if count > 0 {
			return errandFailure("该需求行已分配给其他任务")
		}
		if a.DistributedQuantity != nil && (*a.DistributedQuantity < 0 || *a.DistributedQuantity > d.Quantity) {
			return errandFailure("分发数量超出原需求")
		}
		if snap.TargetStatus == "collecting_payment" {
			if a.DistributedQuantity == nil || i.PurchasedQuantity == nil ||
				(*i.PurchasedQuantity > 0 && i.ActualUnitPriceCents == nil) || i.ActualUnitPriceCents != nil && *i.ActualUnitPriceCents < 0 {
				return errandFailure("恢复收款必须具备完整采购数量、实际单价和分发数量")
			}
			distributed[i.ID] += int64(*a.DistributedQuantity)
			price := int32(0)
			if i.ActualUnitPriceCents != nil {
				price = *i.ActualUnitPriceCents
			}
			billRows = append(
				billRows,
				repository.TaskPaymentBillAssignmentRow{
					AssignmentID:           a.ID,
					PayerID:                a.PurchaserID,
					PayeeID:                snap.Record.OwnerID,
					PackagingFeeCents:      snap.Record.PackagingFeeCents,
					ActualUnitPriceCents:   price,
					DistributedQuantity:    a.DistributedQuantity,
					ServiceFeePerUnitCents: a.ServiceFeePerUnitCents,
				},
			)
		}
	}
	for _, item := range snap.Items {
		if required[item.ID] != int64(item.RequiredQuantity) {
			return errandFailure("全部恢复需求的数量必须与原采购行需求总量一致")
		}
	}
	if len(seen) != len(snap.DemandItems) {
		return errandFailure("整单纠错要求包含关联需求的全部明细，不能修改部分分配")
	}
	if snap.TargetStatus == "collecting_payment" {
		for _, item := range snap.Items {
			if item.PurchasedQuantity == nil || distributed[item.ID] != int64(*item.PurchasedQuantity) {
				return errandFailure("每项采购数量必须与累计分发数量一致")
			}
		}
	}
	if snap.TargetStatus != "collecting_payment" {
		return nil
	}
	if len(billRows) == 0 {
		return errandFailure("没有可恢复的收款参与人")
	}
	drafts, e := buildTaskPaymentBillDrafts(billRows)
	if e != nil {
		return e
	}
	for _, draft := range drafts {
		if draft.PayerID != draft.PayeeID {
			amounts[draft.PayerID] = int64(draft.AmountCents)
		}
	}
	if r.Action == "reverse_completion" {
		payable := false
		for _, amount := range amounts {
			payable = payable || amount > 0
		}
		if !payable {
			return errandFailure("本任务无应收款，不适用撤销收款；撤销整单请使用取消")
		}
	}
	seenBills := map[int64]bool{}
	for _, b := range snap.Bills {
		payer, e := strconv.ParseInt(b.PayerId, 10, 64)
		if e != nil {
			return errandFailure("账单付款方无效")
		}
		amount, ok := amounts[payer]
		if !ok || b.PayeeId != fmt.Sprint(snap.Record.OwnerID) || b.AmountCents != amount || seenBills[payer] {
			return errandFailure("原账单与采购、费用、分配金额不一致")
		}
		seenBills[payer] = true
	}
	for payer, amount := range amounts {
		if !seenBills[payer] && amount > 0 {
			if r.Values["missing_bill_confirmed"] != "true" {
				return errandFailure("缺少原账单，请核验后确认补建缺失账单")
			}
			snap.RestorationBills = append(
				snap.RestorationBills,
				&av.SourceBill{
					PayerId:     fmt.Sprint(payer),
					PayeeId:     fmt.Sprint(snap.Record.OwnerID),
					AmountCents: amount,
				},
			)
		}
	}
	sort.Slice(
		snap.RestorationBills,
		func(i, j int) bool { return snap.RestorationBills[i].PayerId < snap.RestorationBills[j].PayerId },
	)
	return nil
}

func sameErrandJSON(a, b any) bool {
	left, e := json.Marshal(a)
	if e != nil {
		return false
	}
	right, e := json.Marshal(b)
	return e == nil && bytes.Equal(left, right)
}

//nolint:gocyclo // Lock and compare every resource in the frozen graph before reserving it.
func reserveErrandCorrection(ctx context.Context, tx bun.Tx, c *adminflow.Case) error {
	var snap adminErrandSnapshot
	if e := json.Unmarshal(c.Plan.Snapshot, &snap); e != nil {
		return e
	}
	kind := av.ResourceKind(c.Resource)
	row, e := readAdminErrand(ctx, tx, kind, c.ResourceID, true)
	if e != nil {
		return e
	}
	if row.AdminOperationID != "" || row.LifecycleRound != snap.Record.LifecycleRound ||
		!row.UpdatedAt.Equal(snap.Record.UpdatedAt) {
		return adminflow.Failure(connect.CodeAborted, "预览记录已变化")
	}
	if _, e = tx.ExecContext(ctx, "SELECT set_config('sast.admin_operation_id',?,true)", c.ID); e != nil {
		return e
	}
	if kind == av.ResourceKind_RESOURCE_KIND_ERRAND_DEMAND {
		var current []*model.ErrandDemandItem
		if e = tx.NewSelect().
			Model(&current).
			Where("errand_demand_id=?", row.ID).
			Order("id").
			For("UPDATE").
			Scan(ctx); e != nil {
			return e
		}
		if !sameErrandJSON(current, snap.DemandItems) {
			return adminflow.Failure(connect.CodeAborted, "需求行已变化")
		}
		assigned, e := tx.NewSelect().
			TableExpr("errand.errand_task_assignment a").
			Join("JOIN errand.errand_demand_item i ON i.id=a.demand_item_id").
			Where("i.errand_demand_id=?", row.ID).
			Exists(ctx)
		if e != nil {
			return e
		}
		if assigned {
			return adminflow.Failure(connect.CodeAborted, "需求已归属采购分配，请对任务整单纠错")
		}
		_, e = tx.ExecContext(ctx, "UPDATE errand.errand_demand SET admin_operation_id=? WHERE id=?", c.ID, row.ID)
		return e
	}
	var items []*model.ErrandTaskItem
	var assignments []*model.ErrandTaskAssignment
	if e = tx.NewSelect().Model(&items).Where("task_id=?", row.ID).Order("id").For("UPDATE").Scan(ctx); e != nil {
		return e
	}
	if e = tx.NewSelect().Model(&assignments).Where("task_id=?", row.ID).Order("id").For("UPDATE").Scan(ctx); e != nil {
		return e
	}
	if !sameErrandJSON(items, snap.Items) || !sameErrandJSON(assignments, snap.ExistingAssignments) {
		return adminflow.Failure(connect.CodeAborted, "采购行或分配已变化")
	}
	for _, d := range snap.Demands {
		current, e := readAdminErrand(ctx, tx, av.ResourceKind_RESOURCE_KIND_ERRAND_DEMAND, d.Record.ID, true)
		if e != nil {
			return e
		}
		if current.AdminOperationID != "" || current.LifecycleRound != d.Record.LifecycleRound ||
			!current.UpdatedAt.Equal(d.Record.UpdatedAt) ||
			current.TaskID != nil && *current.TaskID != row.ID {
			return adminflow.Failure(connect.CodeAborted, "关联需求已变化或归属其他任务")
		}
		if _, e = tx.ExecContext(
			ctx,
			"UPDATE errand.errand_demand SET admin_operation_id=? WHERE id=?",
			c.ID,
			d.Record.ID,
		); e != nil {
			return e
		}
	}
	for _, i := range snap.DemandItems {
		var current model.ErrandDemandItem
		if e = tx.NewSelect().Model(&current).Where("id=?", i.ID).For("UPDATE").Scan(ctx); e != nil {
			return e
		}
		if !sameErrandJSON(&current, i) {
			return adminflow.Failure(connect.CodeAborted, "关联需求行已变化")
		}
	}
	for _, a := range snap.Assignments {
		occupied, e := tx.NewSelect().
			TableExpr("errand.errand_task_assignment").
			Where("demand_item_id=? AND task_id<>?", a.DemandItemID, row.ID).
			Exists(ctx)
		if e != nil {
			return e
		}
		if occupied {
			return adminflow.Failure(connect.CodeAborted, "恢复需求已被其他任务接单")
		}
	}
	_, e = tx.ExecContext(ctx, "UPDATE errand.errand_task SET admin_operation_id=? WHERE id=?", c.ID, row.ID)
	return e
}

func applyErrandCorrection(ctx context.Context, x *adminflow.Execution) error {
	var snap adminErrandSnapshot
	if e := json.Unmarshal(x.Case.Plan.Snapshot, &snap); e != nil {
		return e
	}
	var payment av.CorrectSourceBillsResponse
	if av.ResourceKind(x.Case.Resource) == av.ResourceKind_RESOURCE_KIND_ERRAND_TASK {
		if e := x.RemoteStep(ctx, "payment", func(ctx context.Context) (any, error) {
			return adminflow.CorrectBills(
				ctx,
				x.Case,
				"errand_task",
				snap.Record.LifecycleRound+1,
				snap.Bills,
				x.Case.Plan.Request.Values["funds_handling"],
				snap.RestorationBills,
			)
		}, &payment); e != nil {
			return e
		}
	}
	return x.LocalStep(ctx, "errand", func(ctx context.Context, tx bun.Tx) (any, error) {
		return finishErrandCorrection(ctx, tx, x, &snap, &payment)
	})
}

//nolint:gocyclo // Preserve the entire task/demand/assignment graph and its history in one commit.
func finishErrandCorrection(
	ctx context.Context,
	tx bun.Tx,
	x *adminflow.Execution,
	snap *adminErrandSnapshot,
	payment *av.CorrectSourceBillsResponse,
) (any, error) {
	c := x.Case
	kind := av.ResourceKind(c.Resource)
	row, e := readAdminErrand(ctx, tx, kind, c.ResourceID, true)
	if e != nil {
		return nil, e
	}
	if row.AdminOperationID != c.ID || row.LifecycleRound != snap.Record.LifecycleRound {
		return nil, adminflow.Failure(connect.CodeAborted, "纠错资源执行权已变化")
	}
	resource := "errand_task"
	if kind == av.ResourceKind_RESOURCE_KIND_ERRAND_DEMAND {
		resource = "errand_demand"
	}
	if _, e = tx.ExecContext(
		ctx,
		`INSERT INTO errand.admin_round_history(resource,resource_id,lifecycle_round,operation_id,snapshot) VALUES (?,?,?,?,?::jsonb)`,
		resource,
		row.ID,
		row.LifecycleRound,
		c.ID,
		string(c.Plan.Snapshot),
	); e != nil {
		return nil, e
	}
	next := row.LifecycleRound + 1
	now := time.Now().UTC().Truncate(time.Microsecond)
	var cancelled *time.Time
	if snap.TargetStatus == "cancelled" {
		cancelled = &now
	}
	if kind == av.ResourceKind_RESOURCE_KIND_ERRAND_DEMAND {
		if _, e = tx.ExecContext(
			ctx,
			`UPDATE errand.errand_demand_item SET status=?,updated_at=? WHERE errand_demand_id=?`,
			snap.TargetStatus,
			now,
			row.ID,
		); e != nil {
			return nil, e
		}
		if _, e = tx.ExecContext(
			ctx,
			`UPDATE errand.errand_demand SET status=?,lifecycle_round=?,admin_operation_id='',deadline=COALESCE(?,deadline),cancelled_at=?,updated_at=? WHERE id=?`,
			snap.TargetStatus,
			next,
			snap.Deadline,
			cancelled,
			now,
			row.ID,
		); e != nil {
			return nil, e
		}
	} else {
		demandStatus := "shopping"
		if snap.TargetStatus == "cancelled" {
			demandStatus = "cancelled"
		}
		if snap.TargetStatus == "collecting_payment" {
			demandStatus = "pending_payment"
		}
		for _, d := range snap.Demands {
			if _, e = tx.ExecContext(
				ctx,
				`INSERT INTO errand.admin_round_history(resource,resource_id,lifecycle_round,operation_id,snapshot) VALUES ('errand_demand',?,?,?,?::jsonb)`,
				d.Record.ID,
				d.Record.LifecycleRound,
				c.ID,
				string(d.Data),
			); e != nil {
				return nil, e
			}
			if _, e = tx.ExecContext(
				ctx,
				`UPDATE errand.errand_demand SET status=?,task_id=?,lifecycle_round=lifecycle_round+1,admin_operation_id='',deadline=COALESCE(?,deadline),shopping_start_at=CASE WHEN ?='shopping' THEN ? ELSE shopping_start_at END,shopping_completed_at=CASE WHEN ?='shopping' THEN NULL ELSE shopping_completed_at END,distribution_completed_at=CASE WHEN ?='shopping' THEN NULL ELSE distribution_completed_at END,payment_completed_at=NULL,cancelled_at=?,updated_at=? WHERE id=? AND admin_operation_id=?`,
				demandStatus,
				row.ID,
				snap.Deadline,
				snap.TargetStatus,
				now,
				snap.TargetStatus,
				snap.TargetStatus,
				cancelled,
				now,
				d.Record.ID,
				c.ID,
			); e != nil {
				return nil, e
			}
			if _, e = tx.ExecContext(
				ctx,
				`UPDATE errand.errand_demand_item SET status=?,updated_at=? WHERE errand_demand_id=?`,
				demandStatus,
				now,
				d.Record.ID,
			); e != nil {
				return nil, e
			}
		}
		bills := map[int64]*av.SourceBill{}
		for _, b := range payment.Bills {
			id, e := admin.ParseID(b.PayerId)
			if e != nil {
				return nil, e
			}
			bills[id] = b
		}
		for _, a := range snap.Assignments {
			copy := *a
			copy.PaymentBillID = nil
			copy.UpdatedAt = now
			if b := bills[a.PurchaserID]; b != nil {
				id, e := admin.ParseID(b.Id)
				if e != nil {
					return nil, e
				}
				copy.PaymentBillID = &id
			}
			if snap.TargetStatus == "shopping" {
				copy.DistributedQuantity = nil
			}
			exists, e := tx.NewSelect().
				TableExpr("errand.errand_task_assignment").
				Where("task_id=? AND demand_item_id=?", row.ID, a.DemandItemID).
				Exists(ctx)
			if e != nil {
				return nil, e
			}
			if exists {
				_, e = tx.NewUpdate().
					Model(&copy).
					Column("distributed_quantity", "payment_bill_id", "updated_at").
					Where("task_id=? AND demand_item_id=?", row.ID, a.DemandItemID).
					Exec(ctx)
			} else {
				copy.ID = 0
				copy.CreatedAt = now
				_, e = tx.NewInsert().Model(&copy).Returning("id").Exec(ctx)
			}
			if e != nil {
				return nil, e
			}
		}
		if snap.TargetStatus == "shopping" {
			if _, e = tx.ExecContext(
				ctx,
				`UPDATE errand.errand_task_item SET purchased_quantity=NULL,non_purchase_reason='',actual_unit_price_cents=NULL,handled_at=NULL,deadline=COALESCE(?,deadline),updated_at=? WHERE task_id=?`,
				snap.Deadline,
				now,
				row.ID,
			); e != nil {
				return nil, e
			}
		}
		if _, e = tx.ExecContext(
			ctx,
			`UPDATE errand.errand_task SET status=?,lifecycle_round=?,admin_operation_id='',shopping_completed_at=CASE WHEN ?='shopping' THEN NULL ELSE shopping_completed_at END,distribution_completed_at=CASE WHEN ?='shopping' THEN NULL ELSE distribution_completed_at END,payment_completed_at=NULL,cancelled_at=?,updated_at=? WHERE id=?`,
			snap.TargetStatus,
			next,
			snap.TargetStatus,
			snap.TargetStatus,
			cancelled,
			now,
			row.ID,
		); e != nil {
			return nil, e
		}
	}
	if e = admin.WriteAudit(
		ctx,
		tx,
		admin.Entry{
			Domain:     "errand",
			ActorID:    c.ExecutorID,
			Action:     c.Action,
			TargetType: resource,
			TargetID:   row.ID,
			RequestID:  c.RequestID,
			Reason:     c.Plan.Request.Reason,
			Evidence:   c.Plan.Request.Evidence,
			Before:     map[string]any{"status": row.Status, "lifecycle_round": row.LifecycleRound},
			After:      map[string]any{"status": snap.TargetStatus, "lifecycle_round": next},
		},
	); e != nil {
		return nil, e
	}
	return map[string]any{"status": snap.TargetStatus, "lifecycle_round": next}, nil
}
