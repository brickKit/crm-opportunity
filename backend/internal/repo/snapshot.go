package repo

import (
	"database/sql"
	"fmt"
)

// UpsertCustomerSnapshotNameTx 维护 mdm-customer 的展示快照——只取 name
// （本组件不关心信用额度，那是 erp-sales 的事，设计计划 §5 的三方分工）。
// 供 backend/internal/consumer 在 besdk.Consume 给的事务里调用（同
// erp-sales 的既有判据：接 *sql.Tx 不自己开 besdk.WithTx，那会开一个新
// 事务和 Consume 已经打开的那个冲突）。
//
// ⚠️ WHERE version < $2 是按 version 单调更新（§3.10）。
func UpsertCustomerSnapshotNameTx(tx *sql.Tx, customerID, name string, version int64) error {
	_, err := tx.Exec(`
		INSERT INTO customer_snapshots (customer_id, name, version)
		VALUES ($1, $2, $3)
		ON CONFLICT (customer_id) DO UPDATE
		   SET name = EXCLUDED.name, version = EXCLUDED.version, updated_at = now()
		 WHERE customer_snapshots.version < EXCLUDED.version`,
		customerID, name, version)
	if err != nil {
		return fmt.Errorf("写 customer_snapshots: %w", err)
	}
	return nil
}
