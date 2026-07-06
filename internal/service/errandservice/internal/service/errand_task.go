package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
)

//联合查询子项和主单，承载单条子项 + 所属主单全量数据库字段，用于所有校验、分组逻辑
type selectedDemandItemRow struct{
	DemandItemID int64 `bun:"demand_item_id"`
	DemandItemUpdateAt time.Time `bun:"demand_item_updated_at"` //乐观锁
	DemandItemStatus model.ErrandDemandItemStatus `bun:"demand_item_status"` //校验open状态
	DemandID int64 `bun:demand_id`
	DemandStatus model.ErrandDemandStatus `bun:"demand_status"`
	RequesterID int64 `bun:"request_id"`
	StoreID int64 `bun:"store_id"`
	ProductTemplateID int64 `bun:"product_template_id"` //聚合task_item
	Quantity int32 `bun:"quantity"`
	ServiveFeePerUnitCents int32 `bun:"service_fee_per_unit_cents"`
	Deadline time.Time `bun:"deadline"`
}
//创建任务时把商品详情存进任务明细，防止后续商品修改导致历史任务展示错乱
type productSnapshotRow struct{
	ID int64 `bun:"id"`
	Tittle string `bun:"tittle"`
	Description string `bun:"description"`
	StoreID int64 `bun:"store_id"`
	MainImageURL string `bun:"main_image_url"` 
}
//同商品+同截止时间合并为一个任务
type taskItemGroupKey struct{
	ProductTemplateID int64
	Deadline time.Time
}

type taskItemGroup struct{
	Snapshot productSnapshotRow
	RequiredQuantity int32 //本组商品总采购量
	Rows []selectedDemandItemRow  //本组所有商品原始需求列表
}

var(
	ErrInvalidDemandItem = errors.New("invalid demand item") //防止前段缓存或未刷新
	ErrConcurrencyConflict = errors.New("concurrency conflict") //乐观锁
	ErrStoreMismatch = errors.New("store mismatch")
	ErrDemandItemNotOpen =  errors.New("demand item not open")
)

func CreateTask(ctx context.Context,captainID int64,req *errandv1.CreateTaskRequest)(int64,error){
	
	//收集id与时间戳
	selectedUpdatedAt := make(map[int64]time.Time,len(req.DemandItems))
	selectIDs := make([]int64,0,len(req.DemandItems))
	for i,item := range req.DemandItems{
		if item == nil{
			return 0,connect.NewError(connect.CodeInvalidArgument,fmt.Errorf("demand_items[%d] is nil", i))

		}

		selectedUpdatedAt[item.ErrandDemandItemId] = item.UpdatedAt.AsTime().UTC()
		selectIDs = append(selectIDs,item.ErrandDemandItemId)
	}

	//开启事务，调用查询参数
	var taskID int64
	err := postgres.DB.RunInTx(ctx,&sql.TxOptions{},func(ctx context.Context, tx bun.Tx) error{
		rows,err  := loadSelectedDemandItemsForUpdate(ctx,tx,selectIDs)
		now := time.Now().UTC()
		demandRows := make(map[int64][]selectedDemandItemRow)
		productIDsSet := make(map[int64]struct{},len(rows))
		
		for _, row := range rows {
			reqUpdatedAt := selectedUpdatedAt[row.DemandItemID]

			demandRows[row.DemandID] = append(demandRows[row.DemandID], row)
			productIDsSet[row.ProductTemplateID] = struct{}{}
		}

		productIDs := make([]int64,0,len(productIDsSet))
		for id := range productIDsSet{
			productIDs = append(productIDs,id)
		}

		snapshots,err := loadProductSnapshots(ctx,tx,productIDs)
		for _,snap := range snapshots{
			if snap.StoreID != req.StoreId{
				return newErrandInternalError("product/store mismatch")
			}
		}

		task := &model.ErrandTask{
			TaskNo: generateTaskNO(),
			CaptainID: captainID,
			StoreID: req.StoreId,
			Status: model.ErrandTaskStatusShopping,
		}
		taskID = task.ID

		grouped := make(map[taskItemGroupKey]*taskItemGroup)
		for _,row := range rows {
			key := taskItemGroupKey{
				ProductTemplateID: row.ProductTemplateID,
				Deadline: row.Deadline.UTC(),
			}

			group,ok := grouped[key]
			if !ok {
				group = &taskItemGroup{
					Snapshot: snapshots[row.ProductTemplateID],
				}
				grouped[key] = group
			}
			group.RequiredQuantity+=row.Quantity
			group.Rows = append(group.Rows, row)
		}

		taskItemByDemandItemID := make(map[int64]int64,len(rows))
		for key,group := range grouped{
			taskItem := &model.ErrandTaskItem{
				TaskID: task.ID,
				ProductTemplateID: key.ProductTemplateID,
				TitleSnapshot: group.Snapshot.Tittle,
				DescriptionSnapshot: group.Snapshot.Description,
				ImageURLSnapshot: group.Snapshot.MainImageURL,
				RequiredQuantity: group.RequiredQuantity,
				Deadline: key.Deadline,
			}

			for _,row := range group.Rows{
				taskItemByDemandItemID[row.DemandItemID] = taskItem.ID
			}
		}


	})
}


func GetShoppingTaskDetail(ctx context.Context,)

