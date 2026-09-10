// Package consumer 消费 mdm.customer.created.v1/.updated.v1（维护
// customer_snapshots 展示字段，设计计划 §4）。⚠️ 不消费 erp-sales 的
// 任何事件——赢单之后订单建成没有、发货没有，本组件一概不追踪，那会
// 让 CRM 反向依赖 ERP 的状态机（§1.4 铁律、设计计划 §4）。
package consumer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	besdk "github.com/brickKit/be-sdk-go"
	"github.com/nats-io/nats.go"

	"github.com/brickKit/crm-opportunity/backend/internal/repo"
)

func Start(ctx context.Context, db *sql.DB, role, schema string, nc *nats.Conn, logger *slog.Logger) error {
	subjects := []struct {
		subject string
		handle  func(context.Context, *sql.Tx, besdk.Event) error
	}{
		{"mdm.customer.created.v1", customerSnapshotHandler()},
		{"mdm.customer.updated.v1", customerSnapshotHandler()},
	}

	errCh := make(chan error, len(subjects))
	for _, s := range subjects {
		s := s
		go func() {
			errCh <- besdk.Consume(ctx, nc, db, role, schema, s.subject, s.handle)
		}()
	}

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err // ⚠️ 返回 error，不许 log.Fatal（§13.3 铁律七）
	}
}

// customerPayload 字段照抄 mdm-customer 已经真实存在的契约
// （contracts/events/customer.events.json）。本组件只取 name——不关心
// 信用额度，那是 erp-sales 的事（设计计划 §5 的三方分工）。
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
