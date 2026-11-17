package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	av "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/adminflow"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/spotservice/internal/repository"
	"github.com/uptrace/bun"
	"google.golang.org/protobuf/proto"
)

var (
	getSpotCorrectionBills = adminflow.GetSourceBills
	correctSpotBills       = adminflow.CorrectBills
)

type spotCorrectionSnapshot struct {
	Order             model.SpotOrder  `json:"order"`
	Goods             model.SpotGoods  `json:"goods"`
	Restored          model.SpotOrder  `json:"restored"`
	Bills             []*av.SourceBill `json:"bills"`
	RestorationBills  []*av.SourceBill `json:"restoration_bills,omitempty"`
	InventoryReleased bool             `json:"inventory_released"`
	StockHandling     string           `json:"stock_handling"`
	FundsHandling     string           `json:"funds_handling"`
}

func SpotCorrectionEngine() *adminflow.Engine {
	return &adminflow.Engine{
		DB:              postgres.DB,
		Domain:          "spot",
		Build:           buildSpotCorrection,
		Reserve:         reserveSpotCorrection,
		RollbackReserve: rollbackSpotCorrectionReserve,
		Apply:           applySpotCorrection,
	}
}

func spotCorrectionActions(order *model.SpotOrder) []string {
	if order.AdminOperationID != "" {
		return nil
	}
	switch order.Status {
	case model.SpotOrderStatusPendingPayment, model.SpotOrderStatusPaid:
		return []string{"cancel"}
	case model.SpotOrderStatusCompleted:
		return []string{"reverse_completion"}
	case model.SpotOrderStatusCancelled:
		return []string{"reopen"}
	}
	return nil
}

func buildSpotCorrection(ctx context.Context, req *av.PreviewCorrectionRequest) (*adminflow.Plan, error) {
	orderPtr, err := loadSpotCorrectionOrder(ctx, req)
	if err != nil {
		return nil, err
	}
	order := *orderPtr
	goods, err := repository.GetSpotGoodsByID(ctx, order.ListingID)
	if err != nil {
		return nil, err
	}
	bills, err := getSpotCorrectionBills(ctx, "spot_order", order.ID, order.LifecycleRound)
	if err != nil {
		return nil, err
	}
	record, err := adminOrderRecord(ctx, postgres.DB, &order)
	if err != nil {
		return nil, err
	}
	snapshot := spotCorrectionSnapshot{
		Order:             order,
		Goods:             *goods,
		Restored:          order,
		Bills:             bills,
		InventoryReleased: order.InventoryState == "released",
		StockHandling:     req.Values["stock_handling"],
		FundsHandling:     req.Values["funds_handling"],
	}
	plan := &adminflow.Plan{
		Record: record,
		Steps: []adminflow.Step{
			{Key: "payment", Description: "核实并处理关联账单", Status: "pending"},
			{Key: "order", Description: "保存历史并恢复订单及库存状态", Status: "pending"},
		},
		Warnings: []string{"操作只登记已核实的款项处理，不会自动转账或退款。"},
	}
	if err := prepareSpotRestoration(&snapshot, req, plan); err != nil {
		return nil, err
	}
	if err := prepareSpotFunds(&snapshot, req, plan); err != nil {
		return nil, err
	}
	if err := prepareSpotInventory(ctx, &snapshot, req, plan); err != nil {
		return nil, err
	}
	addSpotCorrectionImpacts(&snapshot, req, plan)
	plan.Snapshot, err = json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

func prepareSpotRestoration(
	snapshot *spotCorrectionSnapshot,
	req *av.PreviewCorrectionRequest,
	plan *adminflow.Plan,
) error {
	allowed := map[string]bool{
		"funds_handling":                 true,
		"stock_handling":                 true,
		"inventory_released":             true,
		"title":                          true,
		"quantity":                       true,
		"unit_price_cents":               true,
		"missing_bill_confirmed":         true,
		"missing_bill_status":            true,
		"submitted_unreceived_confirmed": true,
	}
	for key := range req.Values {
		if !allowed[key] {
			return invalidAdminInput()
		}
	}
	for _, field := range []spotRestorationField{
		{"title", "商品名称", strings.TrimSpace(snapshot.Restored.TitleSnapshot) == ""},
		{"quantity", "购买数量", snapshot.Restored.Quantity <= 0},
		{"unit_price_cents", "成交单价（分）", snapshot.Restored.UnitPriceCents <= 0},
	} {
		if err := fillSpotRestorationField(snapshot, req, plan, field); err != nil {
			return err
		}
	}
	if err := validateRestoredSpotAmount(&snapshot.Restored, req.Action); err != nil {
		return err
	}
	return prepareSpotInventoryHistory(snapshot, req, plan)
}

type spotRestorationField struct {
	key, label string
	missing    bool
}

func fillSpotRestorationField(
	snapshot *spotCorrectionSnapshot,
	req *av.PreviewCorrectionRequest,
	plan *adminflow.Plan,
	field spotRestorationField,
) error {
	value, present := req.Values[field.key]
	if !field.missing {
		if present {
			return adminflow.Failure(connect.CodeInvalidArgument, "有效的成交快照不能通过恢复表单修改")
		}
		return nil
	}
	if req.Action == "cancel" {
		return nil
	}
	if !present || strings.TrimSpace(value) == "" {
		plan.RestorationFields = append(plan.RestorationFields, spotField(field.key, field.label, ""))
		return nil
	}
	if field.key == "title" {
		if len(value) > 1000 {
			return invalidAdminInput()
		}
		snapshot.Restored.TitleSnapshot = strings.TrimSpace(value)
		return nil
	}
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil || n <= 0 {
		return invalidAdminInput()
	}
	if field.key == "quantity" {
		snapshot.Restored.Quantity = int32(n)
	} else {
		snapshot.Restored.UnitPriceCents = int32(n)
	}
	return nil
}

func validateRestoredSpotAmount(restored *model.SpotOrder, action string) error {
	if action == "cancel" || restored.Quantity <= 0 || restored.UnitPriceCents <= 0 {
		return nil
	}
	total := int64(restored.Quantity) * int64(restored.UnitPriceCents)
	if total > math.MaxInt32 || total < 1 {
		return invalidAdminInput()
	}
	if restored.TotalAmountCents > 0 && total != int64(restored.TotalAmountCents) {
		return adminflow.Failure(connect.CodeInvalidArgument, "补录数量与单价必须与原订单总额一致")
	}
	restored.TotalAmountCents = int32(total)
	return nil
}

func prepareSpotInventoryHistory(
	snapshot *spotCorrectionSnapshot,
	req *av.PreviewCorrectionRequest,
	plan *adminflow.Plan,
) error {
	if req.Action == "reopen" && snapshot.Order.InventoryState == "unverified" {
		switch req.Values["inventory_released"] {
		case "true":
			snapshot.InventoryReleased = true
		case "false":
			snapshot.InventoryReleased = false
		case "":
			plan.RestorationFields = append(
				plan.RestorationFields,
				spotField("inventory_released", "历史是否已回补库存（true / false）", ""),
			)
		default:
			return invalidAdminInput()
		}
	} else if _, ok := req.Values["inventory_released"]; ok {
		return invalidAdminInput()
	}
	return nil
}

func prepareSpotFunds(snapshot *spotCorrectionSnapshot, req *av.PreviewCorrectionRequest, plan *adminflow.Plan) error {
	paid := false
	claimed := false
	refunded := false
	if err := prepareMissingSpotBills(snapshot, req, plan); err != nil {
		return err
	}
	effectiveBills := append(append([]*av.SourceBill{}, snapshot.Bills...), snapshot.RestorationBills...)
	for _, bill := range effectiveBills {
		if err := validateSpotSourceBill(snapshot, bill); err != nil {
			return err
		}
		if bill.Status == "completed" {
			paid = true
		}
		refunded = refunded || bill.RefundRegistered
		if bill.Status == "submitted" {
			claimed = true
		}
	}
	if len(snapshot.Bills) > 1 {
		return adminflow.Failure(connect.CodeFailedPrecondition, "订单关联了多笔账单，需要先核实")
	}

	if err := validateSpotFundsMode(snapshot, req, plan, paid, claimed, refunded); err != nil {
		return err
	}
	if snapshot.FundsHandling == "" {
		return nil
	}
	return admin.ValidateSourceFunds(req.Action, snapshot.FundsHandling, effectiveBills)
}

func validateSpotReopenGoods(goods *model.SpotGoods, quantity int32) error {
	if goods.ClosedAt != nil || goods.StockTotal < 0 || goods.ModerationStatus == "blocked" {
		return adminflow.Failure(connect.CodeFailedPrecondition, "商品当前不可重新占用库存")
	}
	if quantity <= 0 || goods.StockTotal < quantity {
		return adminflow.Failure(connect.CodeFailedPrecondition, "现有库存不足以恢复该订单")
	}
	return nil
}

func reserveSpotCorrection(ctx context.Context, tx bun.Tx, c *adminflow.Case) error {
	if len(c.Plan.RestorationFields) > 0 {
		return adminflow.Failure(connect.CodeFailedPrecondition, "请补齐恢复草稿并重新预览")
	}
	var snapshot spotCorrectionSnapshot
	if err := json.Unmarshal(c.Plan.Snapshot, &snapshot); err != nil {
		return err
	}
	order, goods, err := lockOrderAndGoods(ctx, tx, c.ResourceID)
	if err != nil {
		return err
	}
	if order.AdminOperationID != "" || order.LifecycleRound != snapshot.Order.LifecycleRound ||
		!order.UpdatedAt.Equal(snapshot.Order.UpdatedAt) {
		return adminflow.Failure(connect.CodeAborted, "订单已发生变化，请重新预览")
	}
	if !goods.UpdatedAt.Equal(snapshot.Goods.UpdatedAt) {
		return adminflow.Failure(connect.CodeAborted, "关联库存已发生变化，请重新预览")
	}
	if err := validateSpotBillSnapshot(ctx, &snapshot); err != nil {
		return err
	}
	if err := reserveSpotCorrectionStock(ctx, tx, c, &snapshot, goods); err != nil {
		return err
	}
	if _, err := tx.NewUpdate().
		Model(order).
		Set("admin_operation_id = ?", c.ID).
		Set("updated_at = clock_timestamp()").
		WherePK().
		Exec(ctx); err != nil {
		return err
	}
	return admin.WriteAudit(
		ctx,
		tx,
		admin.Entry{
			Domain:     "spot",
			ActorID:    c.ExecutorID,
			Action:     "correction_started",
			TargetType: "spot_order",
			TargetID:   order.ID,
			RequestID:  c.RequestID,
			Reason:     c.Plan.Request.Reason,
			Evidence:   c.Plan.Request.Evidence,
			Before:     map[string]any{"status": order.Status, "round": order.LifecycleRound},
			After:      map[string]any{"operation_id": c.ID},
		},
	)
}

func reserveSpotCorrectionStock(
	ctx context.Context,
	tx bun.Tx,
	c *adminflow.Case,
	snapshot *spotCorrectionSnapshot,
	goods *model.SpotGoods,
) error {
	if c.Action != "reopen" || !snapshot.InventoryReleased {
		return nil
	}
	if err := validateSpotReopenGoods(goods, snapshot.Restored.Quantity); err != nil {
		return err
	}
	if err := repository.LockCatalogForSale(ctx, tx, goods.StoreID, goods.ProductTemplateID); err != nil {
		return err
	}
	if err := repository.DecreaseSpotGoodsStock(ctx, tx, goods.ID, snapshot.Restored.Quantity); err != nil {
		return err
	}
	return writeCorrectionStockLedger(ctx, tx, c, goods.ID, -snapshot.Restored.Quantity)
}

func validateSpotBillSnapshot(ctx context.Context, snapshot *spotCorrectionSnapshot) error {
	bills, err := getSpotCorrectionBills(ctx, "spot_order", snapshot.Order.ID, snapshot.Order.LifecycleRound)
	if err != nil {
		return err
	}
	if len(bills) != len(snapshot.Bills) {
		return adminflow.Failure(connect.CodeAborted, "关联账单已发生变化，请重新预览")
	}
	for i, bill := range bills {
		if !proto.Equal(bill, snapshot.Bills[i]) {
			return adminflow.Failure(connect.CodeAborted, "关联账单已发生变化，请重新预览")
		}
	}
	return nil
}

func rollbackSpotCorrectionReserve(ctx context.Context, tx bun.Tx, c *adminflow.Case) error {
	var snapshot spotCorrectionSnapshot
	if err := json.Unmarshal(c.Plan.Snapshot, &snapshot); err != nil {
		return err
	}
	if c.Action != "reopen" || !snapshot.InventoryReleased {
		return nil
	}
	order, goods, err := lockOrderAndGoods(ctx, tx, c.ResourceID)
	if err != nil {
		return err
	}
	if order.AdminOperationID != c.ID {
		return nil
	}
	ref := "admin_correction_rollback:" + c.ID
	exists, err := tx.NewSelect().
		Model((*model.SpotStockLedger)(nil)).
		Where("ref_type=? AND ref_id=?", ref, order.ID).
		Exists(ctx)
	if err != nil || exists {
		return err
	}
	if goods.StockTotal < 0 || int64(goods.StockTotal)+int64(snapshot.Restored.Quantity) > math.MaxInt32 {
		return adminflow.Failure(connect.CodeFailedPrecondition, "库存预留暂时无法释放，请核实库存状态")
	}
	if err := repository.IncreaseSpotGoodsStock(ctx, tx, goods.ID, snapshot.Restored.Quantity); err != nil {
		return err
	}
	return repository.InsertStockLedger(ctx, tx, &model.SpotStockLedger{
		ListingID: goods.ID, Delta: snapshot.Restored.Quantity, Reason: model.StockLedgerReasonManualAdjust,
		RefType: ref, RefID: &c.ResourceID, OperatorID: &c.ExecutorID,
	})
}

func applySpotCorrection(ctx context.Context, x *adminflow.Execution) error {
	var snapshot spotCorrectionSnapshot
	if err := json.Unmarshal(x.Case.Plan.Snapshot, &snapshot); err != nil {
		return err
	}
	var payment av.CorrectSourceBillsResponse
	if err := x.RemoteStep(ctx, "payment", func(ctx context.Context) (any, error) {
		return correctSpotBills(
			ctx,
			x.Case,
			"spot_order",
			snapshot.Order.LifecycleRound+1,
			snapshot.Bills,
			snapshot.FundsHandling,
			snapshot.RestorationBills,
		)
	}, &payment); err != nil {
		return err
	}
	return x.LocalStep(ctx, "order", func(ctx context.Context, tx bun.Tx) (any, error) {
		return finalizeSpotCorrection(ctx, tx, x.Case, &snapshot, &payment)
	})
}

func finalizeSpotCorrection(
	ctx context.Context,
	tx bun.Tx,
	c *adminflow.Case,
	snapshot *spotCorrectionSnapshot,
	payment *av.CorrectSourceBillsResponse,
) (any, error) {
	order, goods, err := lockOrderAndGoods(ctx, tx, c.ResourceID)
	if err != nil {
		return nil, err
	}
	if order.AdminOperationID != c.ID || order.LifecycleRound != snapshot.Order.LifecycleRound {
		return nil, adminflow.Failure(connect.CodeAborted, "订单处理轮次已变化")
	}
	if err := restoreCancelledSpotStock(ctx, tx, c, snapshot, goods); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO spot.admin_round_history (resource,resource_id,lifecycle_round,operation_id,snapshot) VALUES ('spot_order',?,?,?,?::jsonb)`,
		order.ID,
		order.LifecycleRound,
		c.ID,
		string(c.Plan.Snapshot),
	)
	if err != nil {
		return nil, err
	}
	if err := restoreSpotOrderState(ctx, tx, c, snapshot, payment, order, goods); err != nil {
		return nil, err
	}
	after, err := getAdminSpotRecord(ctx, tx, av.ResourceKind_RESOURCE_KIND_SPOT_ORDER, order.ID, false)
	if err != nil {
		return nil, err
	}
	before, err := adminOrderRecord(ctx, tx, &snapshot.Order)
	if err != nil {
		return nil, err
	}
	if err := admin.WriteAudit(
		ctx,
		tx,
		admin.Entry{
			Domain:     "spot",
			ActorID:    c.ExecutorID,
			Action:     c.Action,
			TargetType: "spot_order",
			TargetID:   order.ID,
			RequestID:  c.RequestID,
			Reason:     c.Plan.Request.Reason,
			Evidence:   c.Plan.Request.Evidence,
			Before:     adminAuditSnapshot(before),
			After:      adminAuditSnapshot(after),
		},
	); err != nil {
		return nil, err
	}
	return after, nil
}

func writeCorrectionStockLedger(ctx context.Context, tx bun.Tx, c *adminflow.Case, listingID int64, delta int32) error {
	return repository.InsertStockLedger(
		ctx,
		tx,
		&model.SpotStockLedger{
			ListingID:  listingID,
			Delta:      delta,
			Reason:     model.StockLedgerReasonManualAdjust,
			RefType:    "admin_correction:" + c.ID,
			RefID:      &c.ResourceID,
			OperatorID: &c.ExecutorID,
		},
	)
}

func loadSpotCorrectionOrder(ctx context.Context, req *av.PreviewCorrectionRequest) (*model.SpotOrder, error) {
	if req.Resource != av.ResourceKind_RESOURCE_KIND_SPOT_ORDER {
		return nil, invalidAdminInput()
	}
	id, err := admin.ParseID(req.Id)
	if err != nil {
		return nil, err
	}
	var order model.SpotOrder
	if err := postgres.DB.NewSelect().Model(&order).Where("id = ?", id).Scan(ctx); err != nil {
		return nil, adminLookupError(err)
	}
	if order.AdminOperationID != "" {
		return nil, adminflow.Failure(connect.CodeFailedPrecondition, "订单已有正在处理的纠错，请查看处理进度")
	}
	if order.LifecycleRound != req.ExpectedRound || !order.UpdatedAt.Equal(req.ExpectedUpdatedAt.AsTime()) {
		return nil, adminflow.Failure(connect.CodeAborted, "订单已变更，请刷新后重新预览")
	}
	allowed := false
	for _, action := range spotCorrectionActions(&order) {
		if action == req.Action {
			allowed = true
		}
	}
	if !allowed {
		return nil, adminflow.Failure(connect.CodeFailedPrecondition, "当前订单状态不支持该操作")
	}

	return &order, nil
}

func prepareSpotInventory(
	ctx context.Context,
	snapshot *spotCorrectionSnapshot,
	req *av.PreviewCorrectionRequest,
	plan *adminflow.Plan,
) error {
	goods := &snapshot.Goods
	if err := prepareSpotCancelStock(snapshot, req, plan); err != nil {
		return err
	}
	if req.Action == "reopen" && snapshot.InventoryReleased && len(plan.RestorationFields) == 0 {
		if err := validateSpotReopenGoods(goods, snapshot.Restored.Quantity); err != nil {
			return err
		}
		if err := postgres.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return repository.LockCatalogForSale(ctx, tx, goods.StoreID, goods.ProductTemplateID)
		}); err != nil {
			return err
		}
	}
	return nil
}

func addSpotCorrectionImpacts(
	snapshot *spotCorrectionSnapshot,
	req *av.PreviewCorrectionRequest,
	plan *adminflow.Plan,
) {
	goods := &snapshot.Goods
	target := "cancelled"
	if req.Action != "cancel" {
		target = "pending_payment"
		if snapshot.FundsHandling == "retain" ||
			len(snapshot.Bills) == 0 && snapshot.Order.PurchaserID == goods.SellerID {
			target = "paid"
		}
	}
	plan.Impacts = []*av.Impact{
		{
			Resource:    av.ResourceKind_RESOURCE_KIND_SPOT_ORDER,
			ResourceId:  req.Id,
			Description: "保留订单编号并建立新处理轮次",
			Before:      fmt.Sprintf("%s / 第%d轮", snapshot.Order.Status, snapshot.Order.LifecycleRound),
			After:       fmt.Sprintf("%s / 第%d轮", target, snapshot.Order.LifecycleRound+1),
		},
	}
	delta := int32(0)
	if req.Action == "cancel" && snapshot.StockHandling == "restore" {
		delta = snapshot.Restored.Quantity
	}
	if req.Action == "reopen" && snapshot.InventoryReleased {
		delta = -snapshot.Restored.Quantity
	}
	plan.Impacts = append(
		plan.Impacts,
		&av.Impact{
			Resource:    av.ResourceKind_RESOURCE_KIND_SPOT_GOODS,
			ResourceId:  strconv.FormatInt(goods.ID, 10),
			Description: "库存变动",
			Before:      strconv.FormatInt(int64(goods.StockTotal), 10),
			After:       strconv.FormatInt(int64(goods.StockTotal)+int64(delta), 10),
		},
	)
	for _, bill := range snapshot.Bills {
		plan.Impacts = append(
			plan.Impacts,
			&av.Impact{
				Resource:    av.ResourceKind_RESOURCE_KIND_BILL,
				ResourceId:  bill.Id,
				Description: "保留原账单历史并登记本轮款项处理",
				Before:      bill.Status,
				After:       snapshot.FundsHandling,
			},
		)
	}
	if snapshot.Order.InventoryState == "unverified" {
		plan.Warnings = append(plan.Warnings, "历史库存回补记录不完整，恢复前需核实并补录。")
	}
}

func prepareMissingSpotBills(
	snapshot *spotCorrectionSnapshot,
	req *av.PreviewCorrectionRequest,
	plan *adminflow.Plan,
) error {
	if len(snapshot.Bills) == 0 && snapshot.Order.PurchaserID != snapshot.Goods.SellerID {
		if req.Values["missing_bill_confirmed"] != "true" {
			plan.RestorationFields = append(
				plan.RestorationFields,
				spotField("missing_bill_confirmed", "已核实并确认补录缺失账单（true）", ""),
			)
		}
		status := req.Values["missing_bill_status"]
		if status != "unpaid" && status != "completed" {
			if status != "" {
				return invalidAdminInput()
			}
			plan.RestorationFields = append(
				plan.RestorationFields,
				spotField("missing_bill_status", "原账单状态（unpaid / completed）", ""),
			)
		}
		if req.Values["missing_bill_confirmed"] == "true" && (status == "unpaid" || status == "completed") {
			snapshot.RestorationBills = []*av.SourceBill{
				{
					PayerId:        strconv.FormatInt(snapshot.Order.PurchaserID, 10),
					PayeeId:        strconv.FormatInt(snapshot.Goods.SellerID, 10),
					AmountCents:    int64(snapshot.Restored.TotalAmountCents),
					Status:         status,
					LifecycleRound: snapshot.Order.LifecycleRound,
				},
			}
		}
	} else if req.Values["missing_bill_confirmed"] != "" || req.Values["missing_bill_status"] != "" {
		return invalidAdminInput()
	}
	return nil
}

func validateSpotSourceBill(snapshot *spotCorrectionSnapshot, bill *av.SourceBill) error {
	if bill.LifecycleRound != snapshot.Order.LifecycleRound ||
		bill.AmountCents != int64(snapshot.Restored.TotalAmountCents) ||
		bill.PayerId != strconv.FormatInt(snapshot.Order.PurchaserID, 10) ||
		bill.PayeeId != strconv.FormatInt(snapshot.Goods.SellerID, 10) {
		return adminflow.Failure(connect.CodeFailedPrecondition, "关联账单与订单快照不一致，需要先核实账单")
	}
	return nil
}

func validateSpotFundsMode(
	snapshot *spotCorrectionSnapshot,
	req *av.PreviewCorrectionRequest,
	plan *adminflow.Plan,
	paid, claimed, refunded bool,
) error {
	if claimed && snapshot.FundsHandling != "" && snapshot.FundsHandling != "no_payment_received" {
		return adminflow.Failure(connect.CodeFailedPrecondition, "已申报付款需先核查到账，或明确登记未收款依据")
	}
	if snapshot.FundsHandling == "" {
		if paid || claimed {
			plan.RestorationFields = append(plan.RestorationFields, spotField("funds_handling", "款项处理方式", ""))
		} else {
			snapshot.FundsHandling = "no_payment_received"
		}
		return nil
	}
	return checkSpotFundsChoice(snapshot.FundsHandling, req.Action, paid, refunded, len(plan.RestorationFields))
}

func checkSpotFundsChoice(funds, action string, paid, refunded bool, missingFields int) error {
	switch funds {
	case "no_payment_received":
		if paid {
			return adminflow.Failure(connect.CodeFailedPrecondition, "已确认收款不能作为未收款处理")
		}
	case "retain":
		if action == "cancel" || !paid || refunded {
			return adminflow.Failure(connect.CodeFailedPrecondition, "当前操作不能保留已收款")
		}
	case "confirmation_error":
		if !paid || refunded {
			return adminflow.Failure(connect.CodeFailedPrecondition, "只有已确认收款的账单可以登记确认错误")
		}
	case "refunded":
		if !paid && !refunded && missingFields == 0 {
			return adminflow.Failure(connect.CodeFailedPrecondition, "无付款账单不能登记退款")
		}
	default:
		return invalidAdminInput()
	}
	return nil
}

func restoreCancelledSpotStock(
	ctx context.Context,
	tx bun.Tx,
	c *adminflow.Case,
	snapshot *spotCorrectionSnapshot,
	goods *model.SpotGoods,
) error {
	if c.Action == "cancel" && snapshot.StockHandling == "restore" {
		if goods.StockTotal < 0 || int64(goods.StockTotal)+int64(snapshot.Restored.Quantity) > math.MaxInt32 {
			return adminflow.Failure(connect.CodeFailedPrecondition, "库存当前无法回补，请核实后继续")
		}
		if err := repository.IncreaseSpotGoodsStock(ctx, tx, goods.ID, snapshot.Restored.Quantity); err != nil {
			return err
		}
		if err := writeCorrectionStockLedger(ctx, tx, c, goods.ID, snapshot.Restored.Quantity); err != nil {
			return err
		}
	}
	return nil
}

func restoreSpotOrderState(
	ctx context.Context,
	tx bun.Tx,
	c *adminflow.Case,
	snapshot *spotCorrectionSnapshot,
	payment *av.CorrectSourceBillsResponse,
	order *model.SpotOrder,
	goods *model.SpotGoods,
) error {
	now := time.Now().UTC()
	status := model.SpotOrderStatusPendingPayment
	var paidAt, completedAt, cancelledAt *time.Time
	var billID *int64
	if len(payment.Bills) > 1 {
		return errors.New("unexpected spot payment result")
	}
	if len(payment.Bills) == 1 {
		parsed, err := admin.ParseID(payment.Bills[0].Id)
		if err != nil {
			return err
		}
		billID = &parsed
	}
	inventory := "reserved"
	if c.Action == "cancel" {
		status = model.SpotOrderStatusCancelled
		cancelledAt = &now
		if snapshot.StockHandling == "restore" {
			inventory = "released"
		}
	} else if payment.AllPaid || len(payment.Bills) == 0 && order.PurchaserID == goods.SellerID {
		status = model.SpotOrderStatusPaid
		if len(payment.Bills) > 0 {
			paidAt = &now
			if snapshot.FundsHandling == "retain" && snapshot.Order.PaidAt != nil {
				paidAt = snapshot.Order.PaidAt
			}
		}
	}
	_, err := tx.NewUpdate().
		Model(order).
		Set("status = ?", status).
		Set("lifecycle_round = ?", order.LifecycleRound+1).
		Set("admin_operation_id = ''").
		Set("inventory_state = ?", inventory).
		Set("title_snapshot = ?", snapshot.Restored.TitleSnapshot).
		Set("quantity = ?", snapshot.Restored.Quantity).
		Set("unit_price_cents = ?", snapshot.Restored.UnitPriceCents).
		Set("total_amount_cents = ?", snapshot.Restored.TotalAmountCents).
		Set("payment_bill_id = ?", billID).
		Set("paid_at = ?", paidAt).
		Set("completed_at = ?", completedAt).
		Set("cancelled_at = ?", cancelledAt).
		Set("updated_at = clock_timestamp()").
		WherePK().
		Exec(ctx)
	if err != nil {
		return err
	}
	return nil
}

func prepareSpotCancelStock(
	snapshot *spotCorrectionSnapshot,
	req *av.PreviewCorrectionRequest,
	plan *adminflow.Plan,
) error {
	goods := &snapshot.Goods
	if req.Action == "cancel" {
		if snapshot.StockHandling == "" {
			if snapshot.Order.Status == model.SpotOrderStatusPendingPayment {
				snapshot.StockHandling = "restore"
			} else {
				plan.RestorationFields = append(
					plan.RestorationFields,
					spotField("stock_handling", "库存处理：restore 回补 / keep 保留扣减", ""),
				)
			}
		}
		if snapshot.StockHandling != "" && snapshot.StockHandling != "restore" && snapshot.StockHandling != "keep" {
			return invalidAdminInput()
		}
		if snapshot.StockHandling == "restore" &&
			(goods.StockTotal < 0 || int64(goods.StockTotal)+int64(snapshot.Restored.Quantity) > math.MaxInt32) {
			return adminflow.Failure(connect.CodeFailedPrecondition, "当前库存无法回补，请先核实库存或选择保留扣减")
		}
	} else if snapshot.StockHandling != "" {
		return invalidAdminInput()
	}
	return nil
}
