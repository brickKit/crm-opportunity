package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
)

// BackfillResult 是一次 order_id 回填的结果，消费者据此决定记什么日志。
type BackfillResult int

const (
	BackfillSet       BackfillResult = iota // order_id 原来为空，已写入
	BackfillUnchanged                       // order_id 已经是这张订单（重复投递 / 重放）
	BackfillConflict                        // order_id 已经是另一张订单，不覆盖
	BackfillNotOurs                         // 不是本组件的商机（id 不合法或不存在）
)

// BackfillOrderIDTx 在消费订单事件的事务里（besdk.Consume 给的 tx，已切好
// role 与 search_path）给商机写上 order_id。一个商机只转一次单：已经有另一张
// 订单时不覆盖，返回 BackfillConflict 与已有的那张订单 id。
func BackfillOrderIDTx(ctx context.Context, tx *sql.Tx, opportunityID, orderID string) (BackfillResult, string, error) {
	oppID, err := strconv.ParseInt(opportunityID, 10, 64)
	if err != nil {
		return BackfillNotOurs, "", nil
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT order_id FROM opportunities WHERE id = $1 FOR UPDATE`, oppID).Scan(&existing)
	if err == sql.ErrNoRows {
		return BackfillNotOurs, "", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("查 opportunities: %w", err)
	}
	switch existing {
	case orderID:
		return BackfillUnchanged, existing, nil
	case "":
	default:
		return BackfillConflict, existing, nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE opportunities SET order_id = $1, version = version + 1, updated_at = now() WHERE id = $2`,
		orderID, oppID); err != nil {
		return 0, "", fmt.Errorf("回填 opportunities.order_id: %w", err)
	}
	return BackfillSet, "", nil
}
