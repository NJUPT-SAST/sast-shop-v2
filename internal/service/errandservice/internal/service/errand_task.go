package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"crypto/rand"
	"math/big"

	errandv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/errand/v1"
	"connectrpc.com/connect"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/bun/postgres"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/model"
	"github.com/NJUPT-SAST/sast-shop-v2/internal/services/errandservice/internal/repository"

	commonv1 "buf.build/gen/go/sast/sast-shop-v2/protocolbuffers/go/sast/sastshopv2/common/v1"
"github.com/NJUPT-SAST/sast-shop-v2/internal/pkg/rpcerror"

"github.com/uptrace/bun"

"github.com/rs/zerolog/log"
)

//同商品+同截止时间合并为一个任务
type taskItemGroupKey struct{
	ProductTemplateID int64
	Deadline time.Time
}

type taskItemGroup struct{
	Snapshot repository.ProductSnapshotRow
	RequiredQuantity int32 //本组商品总采购量
	Rows []repository.SelectedDemandItemRow  //本组所有商品原始需求列表
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
		rows,_ := repository.LoadSelectedDemandItemsForUpdate(ctx,tx,selectIDs)
		now := time.Now().UTC()
		demandRows := make(map[int64][]repository.SelectedDemandItemRow)
		productIDsSet := make(map[int64]struct{},len(rows))
		
		for _, row := range rows {
			//reqUpdatedAt := selectedUpdatedAt[row.DemandItemID]

			demandRows[row.DemandID] = append(demandRows[row.DemandID], row)
			productIDsSet[row.ProductTemplateID] = struct{}{}
		}

		productIDs := make([]int64,0,len(productIDsSet))
		for id := range productIDsSet{
			productIDs = append(productIDs,id)
		}

		snapshots,_ := repository.LoadProductSnapshots(ctx,tx,productIDs)
		for _,snap := range snapshots{
			if snap.StoreID != req.StoreId{
				return newErrandInternalError("product/store mismatch")
			}
		}

		task := &model.ErrandTask{
			TaskNo: generateTaskNo(),
			CaptainID: captainID,
			StoreID: req.StoreId,
			Status: model.ErrandTaskStatusShopping,
		}
		//taskID = task.ID

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
				TitleSnapshot: group.Snapshot.Title,
				DescriptionSnapshot: group.Snapshot.Description,
				ImageURLSnapshot: group.Snapshot.MainImageURL,
				RequiredQuantity: group.RequiredQuantity,
				Deadline: key.Deadline,
			}

			for _,row := range group.Rows{
				taskItemByDemandItemID[row.DemandItemID] = taskItem.ID
			}
		}

		assignments := make([]*model.ErrandTaskAssignment, 0, len(rows))
		for _, row := range rows {
			assignments = append(assignments, &model.ErrandTaskAssignment{
				TaskID:                 task.ID,
				TaskItemID:             taskItemByDemandItemID[row.DemandItemID],
				DemandItemID:           row.DemandItemID,
				PurchaserID:            row.RequesterID,
				ServiceFeePerUnitCents: row.ServiceFeePerUnitCents,
			})
		}

		demandIDs := make([]int64, 0, len(demandRows))
		for demandID := range demandRows {
			demandIDs = append(demandIDs, demandID)
		}
		itemCountByDemandID, _ := repository.LoadDemandItemCounts(ctx, tx, demandIDs)

		for demandID, selectedRows := range demandRows {
			totalCount, ok := itemCountByDemandID[demandID]
			if !ok || totalCount == 0 {
				return newErrandInternalError("demand item count missing")
			}

			selectedItemIDs := demandItemIDs(selectedRows)

			if totalCount == len(selectedRows) {
				if err := repository.UpdateDemandToShopping(ctx, tx, demandID, task.ID, now); err != nil {
					log.Error().Err(err).Msg("failed to update full-selected demand")
					return newErrandInternalError("")
				}
				if err := repository.UpdateDemandItemsToShopping(ctx, tx, selectedItemIDs, now); err != nil {
					log.Error().Err(err).Msg("failed to update full-selected demand items")
					return newErrandInternalError("")
				}
				continue
			}

		base := selectedRows[0]
			taskIDCopy := task.ID
			demandIDCopy := demandID
			splitDemand := &model.ErrandDemand{
				RequesterID:       base.RequesterID,
				StoreID:           base.StoreID,
				Status:            model.ErrandDemandStatusShopping,
				Deadline:          base.Deadline,
				TaskID:            &taskIDCopy,
				SplitFromDemandID: &demandIDCopy,
				ShoppingStartAt:   &now,
			}
			if err := repository.CreateDemand(ctx, tx, splitDemand); err != nil {
				log.Error().Err(err).Msg("failed to create split demand")
				return newErrandInternalError("")
			}
			
		}
		

	return nil	

	})

	if err != nil {
    return 0, err
}

	return taskID,nil
}




func demandItemIDs(rows []repository.SelectedDemandItemRow) []int64 {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.DemandItemID)
	}
	return ids
}

func generateTaskNo() string {
	ts := time.Now().Format("20060102150405")
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "ET" + ts + fmt.Sprintf("%06d", time.Now().UnixNano()%1_000_000)
	}
	return "ET" + ts + fmt.Sprintf("%06d", n.Int64())
}






func newErrandInternalError(msg string) error {
	return rpcerror.NewInternalError(&commonv1.BusinessError_ErrandError{
		ErrandError: &errandv1.ErrandError{
			Code: errandv1.ErrandErrorCode_ERRAND_ERROR_CODE_INTERNAL_ERROR,
		},
	}, msg)
}
