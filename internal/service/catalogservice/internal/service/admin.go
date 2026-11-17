package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	adminv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/admin/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/admin"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/search"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/catalogservice/internal/repository"
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
	switch req.Status {
	case "", "active", "draft", "hidden", "removed", "allowed", "blocked":
	default:
		return nil, invalidAdminInput()
	}
	response := &adminv1.ListRecordsResponse{Page: page, PageSize: size}
	var query *bun.SelectQuery
	switch req.Resource {
	case adminv1.ResourceKind_RESOURCE_KIND_STORE:
		var stores []*model.CatalogStore
		query = postgres.DB.NewSelect().Model(&stores)
		query = filterAdminCatalog(query, "cs", "name", keyword, req, owner)
		count, err := query.OrderExpr("cs.id DESC").Limit(int(size)).Offset(int(page-1) * int(size)).ScanAndCount(ctx)
		if err != nil {
			return nil, err
		}
		response.TotalCount = int64(count)
		for _, store := range stores {
			response.Records = append(response.Records, adminStoreRecord(store))
		}
	case adminv1.ResourceKind_RESOURCE_KIND_PRODUCT:
		var products []*model.CatalogProductTemplate
		query = postgres.DB.NewSelect().Model(&products)
		query = filterAdminCatalog(query, "cpt", "title", keyword, req, owner)
		count, err := query.OrderExpr("cpt.id DESC").Limit(int(size)).Offset(int(page-1) * int(size)).ScanAndCount(ctx)
		if err != nil {
			return nil, err
		}
		response.TotalCount = int64(count)
		for _, product := range products {
			record, err := adminProductRecord(ctx, postgres.DB, product)
			if err != nil {
				return nil, err
			}
			response.Records = append(response.Records, record)
		}
	default:
		return nil, invalidAdminInput()
	}
	return response, nil
}

func filterAdminCatalog(
	query *bun.SelectQuery,
	alias, title, keyword string,
	req *adminv1.ListRecordsRequest,
	owner int64,
) *bun.SelectQuery {
	if keyword != "" {
		query = query.Where(
			"(strpos(lower(?.?), lower(?)) > 0 OR ?.?::text = ?)",
			bun.Ident(alias),
			bun.Ident(title),
			keyword,
			bun.Ident(alias),
			bun.Ident("id"),
			keyword,
		)
	}
	if owner != 0 {
		query = query.Where("?.created_by_user_id = ?", bun.Ident(alias), owner)
	}
	if req.Status == "blocked" || req.Status == "allowed" {
		query = query.Where("?.moderation_status = ?", bun.Ident(alias), req.Status)
	} else if req.Status != "" {
		query = query.Where(
			"?.status = ? AND ?.moderation_status = 'allowed'",
			bun.Ident(alias),
			req.Status,
			bun.Ident(alias),
		)
	}
	if req.CreatedFrom != nil {
		query = query.Where("?.created_at >= ?", bun.Ident(alias), req.CreatedFrom.AsTime())
	}
	if req.CreatedTo != nil {
		query = query.Where("?.created_at <= ?", bun.Ident(alias), req.CreatedTo.AsTime())
	}
	return query
}

func GetAdminRecord(ctx context.Context, req *adminv1.GetRecordRequest) (*adminv1.GetRecordResponse, error) {
	if _, err := admin.Require(ctx); err != nil {
		return nil, err
	}
	id, err := admin.ParseID(req.Id)
	if err != nil {
		return nil, err
	}
	record, err := getAdminCatalogRecord(ctx, postgres.DB, req.Resource, id, false)
	if err != nil {
		return nil, err
	}
	return &adminv1.GetRecordResponse{Record: record}, nil
}

func getAdminCatalogRecord(
	ctx context.Context,
	db bun.IDB,
	resource adminv1.ResourceKind,
	id int64,
	lock bool,
) (*adminv1.AdminRecord, error) {
	switch resource {
	case adminv1.ResourceKind_RESOURCE_KIND_STORE:
		var item model.CatalogStore
		q := db.NewSelect().Model(&item).Where("id = ?", id)
		if lock {
			q = q.For("UPDATE")
		}
		if err := q.Scan(ctx); err != nil {
			return nil, adminLookupError(err)
		}
		return adminStoreRecord(&item), nil
	case adminv1.ResourceKind_RESOURCE_KIND_PRODUCT:
		var item model.CatalogProductTemplate
		q := db.NewSelect().Model(&item).Where("id = ?", id)
		if lock {
			q = q.For("UPDATE")
		}
		if err := q.Scan(ctx); err != nil {
			return nil, adminLookupError(err)
		}
		return adminProductRecord(ctx, db, &item)
	default:
		return nil, invalidAdminInput()
	}
}

func MutateAdminRecord(ctx context.Context, req *adminv1.MutateRecordRequest) (*adminv1.MutateRecordResponse, error) {
	actor, err := admin.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, values, err := validateCatalogMutationRequest(req)
	if err != nil {
		return nil, err
	}
	mutation := admin.Mutation{
		Domain:    "catalog",
		ActorID:   actor.UserID,
		Action:    req.Resource.String() + "." + req.Action,
		RequestID: req.RequestId,
		Input:     req,
	}
	var response adminv1.MutateRecordResponse
	err = postgres.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return mutateCatalogInTx(ctx, tx, req, values, id, mutation, &response)
	})
	return &response, err
}

func validateAdminCatalogValues(req *adminv1.MutateRecordRequest) (map[string]string, error) {
	if req.Action == "block" || req.Action == "unblock" {
		if len(req.Values) != 0 {
			return nil, invalidAdminInput()
		}
		return nil, nil
	}
	permitted := map[string]bool{"name": true, "address": true, "logo_url": true, "theme_color": true}
	required := []string{"name", "address"}
	if req.Resource == adminv1.ResourceKind_RESOURCE_KIND_PRODUCT {
		permitted = map[string]bool{
			"title":          true,
			"description":    true,
			"price_cents":    true,
			"store_id":       true,
			"main_image_url": true,
			"barcode":        true,
		}
		required = []string{"title", "price_cents", "store_id", "barcode"}
	}
	if len(req.Values) == 0 {
		return nil, invalidAdminInput()
	}
	values := make(map[string]string, len(req.Values))
	for key, value := range req.Values {
		if !permitted[key] || len(value) > 8000 || strings.ContainsRune(value, '\x00') {
			return nil, invalidAdminInput()
		}
		value = strings.TrimSpace(value)
		if err := validateCatalogField(key, value); err != nil {
			return nil, err
		}
		values[key] = value
	}
	if req.Action == "create" {
		for _, key := range required {
			if _, ok := values[key]; !ok {
				return nil, invalidAdminInput()
			}
		}
	}
	return values, nil
}

func applyAdminCatalogMutation(
	ctx context.Context,
	tx bun.Tx,
	req *adminv1.MutateRecordRequest,
	values map[string]string,
	id, actorID int64,
) (int64, error) {
	table := "catalog.catalog_store"
	if req.Resource == adminv1.ResourceKind_RESOURCE_KIND_PRODUCT {
		table = "catalog.catalog_product_template"
	}
	if req.Action == "block" || req.Action == "unblock" {
		status := "blocked"
		if req.Action == "unblock" {
			status = "allowed"
		}
		_, err := tx.NewUpdate().
			TableExpr(table).
			Set("moderation_status = ?", status).
			Set("moderation_reason = ?", strings.TrimSpace(req.Reason)).
			Set("moderation_by = ?", actorID).
			Set("moderation_at = ?", time.Now()).
			Set("updated_at = ?", time.Now()).
			Where("id = ?", id).
			Exec(ctx)
		return id, err
	}
	if req.Resource == adminv1.ResourceKind_RESOURCE_KIND_STORE {
		return applyAdminStoreMutation(ctx, tx, req, values, id, actorID)
	}
	return applyAdminProductMutation(ctx, tx, req, values, id, actorID)
}

func applyAdminStoreMutation(
	ctx context.Context,
	tx bun.Tx,
	req *adminv1.MutateRecordRequest,
	values map[string]string,
	id, actorID int64,
) (int64, error) {
	if req.Action == "create" {
		color := values["theme_color"]
		if color == "" {
			color = "#FFFFFF"
		}
		item := &model.CatalogStore{
			Name:            values["name"],
			Address:         values["address"],
			LogoURL:         values["logo_url"],
			ThemeColor:      color,
			Status:          model.CatalogStatusActive,
			CreatedByUserID: actorID,
		}
		_, err := tx.NewInsert().Model(item).Returning("*").Exec(ctx)
		return item.ID, err
	}
	updates := map[string]any{"updated_at": time.Now()}
	for k, v := range values {
		updates[k] = v
	}
	return id, repository.UpdateStore(ctx, tx, id, updates)
}

func applyAdminProductMutation(
	ctx context.Context,
	tx bun.Tx,
	req *adminv1.MutateRecordRequest,
	values map[string]string,
	id, actorID int64,
) (int64, error) {
	storeID := int64(0)
	if text, ok := values["store_id"]; ok {
		var err error
		storeID, err = admin.ParseID(text)
		if err != nil {
			return 0, err
		}
		if err := requireStoreAvailable(ctx, tx, storeID); err != nil {
			return 0, err
		}
	}
	if req.Action == "create" {
		price, err := strconv.ParseInt(values["price_cents"], 10, 32)
		if err != nil {
			return 0, invalidAdminInput()
		}
		item := &model.CatalogProductTemplate{
			Title:           values["title"],
			Description:     values["description"],
			PriceCents:      int32(price),
			StoreID:         storeID,
			Status:          model.CatalogStatusActive,
			CreatedByUserID: actorID,
		}
		if err := repository.CreateProductTemplate(ctx, tx, item); err != nil {
			return 0, err
		}
		id = item.ID
	} else {
		updates := map[string]any{"updated_at": time.Now()}
		for k, v := range values {
			switch k {
			case "barcode", "main_image_url":
				continue
			case "store_id", "price_cents":
				parsed, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					return 0, invalidAdminInput()
				}
				updates[k] = parsed
			default:
				updates[k] = v
			}
		}
		if err := repository.UpdateProductTemplate(ctx, tx, id, updates); err != nil {
			return 0, err
		}
	}
	if err := updateAdminProductRelations(ctx, tx, id, values); err != nil {
		return 0, err
	}
	return id, nil
}

func adminStoreRecord(item *model.CatalogStore) *adminv1.AdminRecord {
	status := string(item.Status)
	if item.ModerationStatus == "blocked" {
		status = "blocked"
	}
	return &adminv1.AdminRecord{
		Resource:       adminv1.ResourceKind_RESOURCE_KIND_STORE,
		Id:             strconv.FormatInt(item.ID, 10),
		Title:          item.Name,
		Status:         status,
		OwnerId:        strconv.FormatInt(item.CreatedByUserID, 10),
		CreatedAt:      timestamppb.New(item.CreatedAt),
		UpdatedAt:      timestamppb.New(item.UpdatedAt),
		LifecycleRound: 1,
		Fields: []*adminv1.RecordField{
			catalogField("name", "店铺名称", item.Name),
			catalogField("address", "地址", item.Address),
			catalogField("logo_url", "店铺图片", item.LogoURL),
			catalogField("theme_color", "主题色", item.ThemeColor),
			catalogField("status", "业务状态", string(item.Status)),
			catalogField("moderation_status", "治理状态", item.ModerationStatus),
			catalogField("moderation_reason", "治理原因", item.ModerationReason),
		},
		AllowedActions: catalogActions(item.ModerationStatus),
	}
}

func adminProductRecord(
	ctx context.Context,
	db bun.IDB,
	item *model.CatalogProductTemplate,
) (*adminv1.AdminRecord, error) {
	var barcode model.CatalogProductBarcode
	err := db.NewSelect().
		Model(&barcode).
		Where("product_template_id = ?", item.ID).
		OrderExpr("id ASC").
		Limit(1).
		Scan(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var image model.CatalogProductImage
	err = db.NewSelect().
		Model(&image).
		Where("product_template_id = ?", item.ID).
		OrderExpr("sort_order ASC, id ASC").
		Limit(1).
		Scan(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	amount := int64(item.PriceCents)
	status := string(item.Status)
	if item.ModerationStatus == "blocked" {
		status = "blocked"
	}
	return &adminv1.AdminRecord{
		Resource:       adminv1.ResourceKind_RESOURCE_KIND_PRODUCT,
		Id:             strconv.FormatInt(item.ID, 10),
		Title:          item.Title,
		Status:         status,
		OwnerId:        strconv.FormatInt(item.CreatedByUserID, 10),
		CreatedAt:      timestamppb.New(item.CreatedAt),
		UpdatedAt:      timestamppb.New(item.UpdatedAt),
		LifecycleRound: 1,
		AmountCents:    &amount,
		Fields: []*adminv1.RecordField{
			catalogField("title", "商品名称", item.Title),
			catalogField("description", "描述", item.Description),
			catalogField("price_cents", "价格（分）", strconv.FormatInt(amount, 10)),
			catalogField("store_id", "店铺编号", strconv.FormatInt(item.StoreID, 10)),
			catalogField("main_image_url", "商品图片", image.ImageURL),
			catalogField("barcode", "条码", barcode.Barcode),
			catalogField("status", "业务状态", string(item.Status)),
			catalogField("moderation_status", "治理状态", item.ModerationStatus),
			catalogField("moderation_reason", "治理原因", item.ModerationReason),
		},
		AllowedActions: catalogActions(item.ModerationStatus),
	}, nil
}

func catalogField(key, label, value string) *adminv1.RecordField {
	return &adminv1.RecordField{Key: key, Label: label, Value: value}
}

func catalogActions(status string) []string {
	if status == "blocked" {
		return []string{"update", "unblock"}
	}
	return []string{"update", "block"}
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
	stores, err := postgres.DB.NewSelect().Model((*model.CatalogStore)(nil)).Count(ctx)
	if err != nil {
		return nil, err
	}
	products, err := postgres.DB.NewSelect().Model((*model.CatalogProductTemplate)(nil)).Count(ctx)
	if err != nil {
		return nil, err
	}
	blocked, err := postgres.DB.NewSelect().
		Model((*model.CatalogProductTemplate)(nil)).
		Where("moderation_status = ?", "blocked").
		Count(ctx)
	if err != nil {
		return nil, err
	}
	return &adminv1.GetStatsResponse{
		Metrics: []*adminv1.Metric{
			{Key: "stores", Label: "店铺", Value: int64(stores)},
			{Key: "products", Label: "商品模板", Value: int64(products)},
			{Key: "blocked_products", Label: "已停用模板", Value: int64(blocked)},
		},
	}, nil
}

func adminAuditSnapshot(record *adminv1.AdminRecord) map[string]string {
	if record == nil {
		return nil
	}
	snapshot := map[string]string{
		"id":         record.Id,
		"title":      record.Title,
		"status":     record.Status,
		"owner_id":   record.OwnerId,
		"updated_at": record.UpdatedAt.AsTime().UTC().Format(time.RFC3339Nano),
	}
	for _, field := range record.Fields {
		snapshot[field.Key] = field.Value
	}
	return snapshot
}

func validateCatalogMutationRequest(req *adminv1.MutateRecordRequest) (int64, map[string]string, error) {
	if err := admin.ValidateReason(req.Reason, req.Evidence); err != nil {
		return 0, nil, err
	}
	switch req.Resource {
	case adminv1.ResourceKind_RESOURCE_KIND_STORE, adminv1.ResourceKind_RESOURCE_KIND_PRODUCT:
	default:
		return 0, nil, invalidAdminInput()
	}
	switch req.Action {
	case "create", "update", "block", "unblock":
	default:
		return 0, nil, invalidAdminInput()
	}
	values, err := validateAdminCatalogValues(req)
	if err != nil {
		return 0, nil, err
	}
	if req.Action == "create" {
		if req.Id != "" {
			return 0, nil, invalidAdminInput()
		}
		return 0, values, nil
	}
	id, err := admin.ParseID(req.Id)
	if err != nil {
		return 0, nil, err
	}
	if req.ExpectedUpdatedAt == nil || !req.ExpectedUpdatedAt.IsValid() || req.ExpectedRound != 1 {
		return 0, nil, invalidAdminInput()
	}
	return id, values, nil
}

func mutateCatalogInTx(
	ctx context.Context,
	tx bun.Tx,
	req *adminv1.MutateRecordRequest,
	values map[string]string,
	id int64,
	mutation admin.Mutation,
	response *adminv1.MutateRecordResponse,
) error {
	if _, err := admin.RequireCurrent(ctx, tx, false); err != nil {
		return err
	}
	result, err := admin.BeginMutation(ctx, tx, mutation)
	if err != nil {
		return err
	}
	if result.Replay {
		return json.Unmarshal(result.Response, response)
	}
	var before *adminv1.AdminRecord
	if req.Action != "create" {
		before, err = getAdminCatalogRecord(ctx, tx, req.Resource, id, true)
		if err != nil {
			return err
		}
		if !before.UpdatedAt.AsTime().Equal(req.ExpectedUpdatedAt.AsTime()) {
			return connect.NewError(connect.CodeAborted, errors.New("记录已变更，请刷新后重试"))
		}
	}
	id, err = applyAdminCatalogMutation(ctx, tx, req, values, id, mutation.ActorID)
	if err != nil {
		return err
	}
	after, err := getAdminCatalogRecord(ctx, tx, req.Resource, id, false)
	if err != nil {
		return err
	}
	if err := admin.WriteAudit(
		ctx,
		tx,
		admin.Entry{
			Domain:     "catalog",
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
	response.Record = after
	return admin.CompleteMutation(ctx, tx, mutation, response)
}

func validateCatalogField(key, value string) error {
	switch key {
	case "name", "title", "barcode":
		if value == "" || len(value) > 1000 {
			return invalidAdminInput()
		}
	case "price_cents":
		return validateCatalogPrice(value)
	case "store_id":
		if _, err := admin.ParseID(value); err != nil {
			return err
		}
	case "logo_url", "main_image_url":
		if value != "" && !ValidatePublicURL(value) {
			return invalidAdminInput()
		}
	case "theme_color":
		if len(value) != 7 || value[0] != '#' {
			return invalidAdminInput()
		}
		if _, err := strconv.ParseUint(value[1:], 16, 24); err != nil {
			return invalidAdminInput()
		}
	}
	return nil
}

func validateCatalogPrice(value string) error {
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil || n <= 0 {
		return invalidAdminInput()
	}
	return nil
}

func updateAdminProductRelations(ctx context.Context, tx bun.Tx, id int64, values map[string]string) error {
	if barcode, ok := values["barcode"]; ok {
		if err := repository.UpsertBarcodeByProductTemplateID(ctx, tx, id, barcode); err != nil {
			return err
		}
	}
	if image, ok := values["main_image_url"]; ok {
		if image == "" {
			if err := repository.DeleteImagesByProductTemplateID(ctx, tx, id); err != nil {
				return err
			}
		} else if err := repository.UpsertImageByProductTemplateID(ctx, tx, id, image); err != nil {
			return err
		}
	}
	return nil
}
