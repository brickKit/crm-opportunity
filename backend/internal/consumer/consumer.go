// Package consumer 消费两类别人的事件：mdm/customer 的客户事件（维护客户名
// 展示快照），erp/sales 的订单创建事件（回填赢单商机的 order_id）。
//
// 只读事件，不追踪订单的状态机：订单确认、发货、取消都归 erp/sales。本组件从
// 订单事件里只取"这个商机转出了哪张订单"这一个事实，CRM 与 ERP 之间仍然只有
// 事件、没有同步调用。
package consumer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	besdk "github.com/brickKit/be-sdk-go"
	"github.com/nats-io/nats.go"

	"github.com/brickKit/crm-opportunity/v2/backend/internal/repo"
)

// subjects 是消费的 subject 名。生产代码用 productionSubjects；测试换成测试
// 私有的名字，免得本机正在跑的组件容器（订阅的是生产 subject）抢走测试消息。
type subjects struct {
	CustomerCreated string
	CustomerUpdated string
	OrderCreated    string
}

var productionSubjects = subjects{
	CustomerCreated: "mdm.customer.created.v1",
	CustomerUpdated: "mdm.customer.updated.v1",
	// 不带 erp. 域前缀是这个 subject 发布时的名字；改名等于删掉旧 subject，不改。
	OrderCreated: "sales.order.created.v1",
}

func Start(ctx context.Context, db *sql.DB, role, schema string, nc *nats.Conn, logger *slog.Logger) error {
	return start(ctx, db, role, schema, nc, logger, productionSubjects)
}

func start(ctx context.Context, db *sql.DB, role, schema string, nc *nats.Conn, logger *slog.Logger, s subjects) error {
	if logger == nil {
		logger = slog.Default()
	}
	handlers := []struct {
		subject string
		handle  func(context.Context, *sql.Tx, besdk.Event) error
	}{
		{s.CustomerCreated, customerSnapshotHandler()},
		{s.CustomerUpdated, customerSnapshotHandler()},
		{s.OrderCreated, orderCreatedHandler(logger)},
	}

	errCh := make(chan error, len(handlers))
	for _, h := range handlers {
		h := h
		go func() {
			errCh <- besdk.Consume(ctx, nc, db, role, schema, h.subject, h.handle)
		}()
	}

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err // 返回 error，不 log.Fatal：进外壳后一个成员退出进程，所有成员一起没了
	}
}

// customerPayload 只取 name：本组件只关心展示名，信用额度是 erp/sales 的事。
type customerPayload struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func customerSnapshotHandler() func(context.Context, *sql.Tx, besdk.Event) error {
	return func(_ context.Context, tx *sql.Tx, ev besdk.Event) error {
		var p customerPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return fmt.Errorf("解析 %s payload: %w", ev.Subject, err)
		}
		return repo.UpsertCustomerSnapshotNameTx(tx, p.ID, p.Name, ev.Version)
	}
}

// orderCreatedPayload 只取两个字段，其余字段一概不读（宽容反序列化：字段缺失
// 就是空串）。source_opportunity_id 为空表示这张订单不是从商机转来的。
type orderCreatedPayload struct {
	OrderID             string `json:"order_id"`
	SourceOpportunityID string `json:"source_opportunity_id"`
}

func orderCreatedHandler(logger *slog.Logger) func(context.Context, *sql.Tx, besdk.Event) error {
	return func(ctx context.Context, tx *sql.Tx, ev besdk.Event) error {
		var p orderCreatedPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return fmt.Errorf("解析 %s payload: %w", ev.Subject, err)
		}
		if p.SourceOpportunityID == "" || p.OrderID == "" {
			return nil // 不是从商机转来的订单
		}
		result, existing, err := repo.BackfillOrderIDTx(ctx, tx, p.SourceOpportunityID, p.OrderID)
		if err != nil {
			return err
		}
		switch result {
		case repo.BackfillSet:
			logger.Info("商机已回填订单 id", "opportunity_id", p.SourceOpportunityID, "order_id", p.OrderID)
		case repo.BackfillConflict:
			// 一个商机只转一次单。第二张订单声称来自同一个商机是数据问题，要人去看，
			// 不能悄悄覆盖第一张——覆盖了，第一张订单就和商机断了联系。
			logger.Error("商机已关联另一张订单，不覆盖", "opportunity_id", p.SourceOpportunityID,
				"existing_order_id", existing, "order_id", p.OrderID)
		case repo.BackfillNotOurs:
			logger.Debug("订单事件里的商机不属于本组件，忽略", "source_opportunity_id", p.SourceOpportunityID,
				"order_id", p.OrderID)
		}
		return nil
	}
}
