package repo

import (
	"database/sql"
	"fmt"
)

// UpsertCustomerSnapshotNameTx 维护 mdm-customer 的展示快照——只取 name
// （本组件不关心信用额度，那是 erp/sales 的事）。供 backend/internal/consumer
// 在 besdk.Consume 给的事务里调用：接 *sql.Tx，不自己开 besdk.WithTx——那会
// 开一个新事务，和 Consume 已经打开的那个各自提交，inbox 去重就不再原子。
//
// ⚠️ WHERE version < $2 是按 version 单调更新：乱序晚到的旧事件不覆盖新值。
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
