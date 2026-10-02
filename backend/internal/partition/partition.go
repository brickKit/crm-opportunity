// Package partition 是 Module.Start 的后台循环之一：为 event_outbox/
// event_inbox 自动创建未来的周分区（迁移只建了头几周）。
//
// ⚠️ opportunities/opportunity_items/opportunity_stage_history 不分区
// （一个销售机会一行，量级小），所以本组件没有月分区维护——只有这一个循环。
// 漏了这个循环，迁移建好的几周分区用完后 INSERT 直接失败，报错是
// PostgreSQL 原生的"no partition of relation found for row"，看不出根因。
package partition

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	besdk "github.com/brickKit/be-sdk-go"
)

const (
	checkInterval  = 24 * time.Hour
	lookAheadWeeks = 4 // 提前建好当前周 + 未来 4 周，留足缓冲
)

var weeklyPartitionedTables = []string{"event_outbox", "event_inbox"}

// Start 立刻检查一次，之后每 24 小时检查一次。单次检查失败只记日志，
// 不让整个循环退出：循环返回 error 会让 Start 退出，合进外壳时带走全部成员。
func Start(ctx context.Context, db *sql.DB, role, schema string, logger *slog.Logger) error {
	if err := ensureAllWeekly(ctx, db, role, schema); err != nil && ctx.Err() == nil {
		logger.Error("周分区维护失败", "error", err)
	}

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := ensureAllWeekly(ctx, db, role, schema); err != nil && ctx.Err() == nil {
				logger.Error("周分区维护失败", "error", err)
			}
		}
	}
}

func ensureAllWeekly(ctx context.Context, db *sql.DB, role, schema string) error {
	return besdk.WithTx(ctx, db, role, schema, func(tx *sql.Tx) error {
		weekStart := mondayOf(time.Now().UTC())
		for i := 0; i <= lookAheadWeeks; i++ {
			from := weekStart.AddDate(0, 0, 7*i)
			to := from.AddDate(0, 0, 7)
			for _, table := range weeklyPartitionedTables {
				if err := ensurePartition(ctx, tx, table, from, to); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func mondayOf(t time.Time) time.Time {
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return d.AddDate(0, 0, -(weekday - 1))
}

// ensurePartition 用 to_regclass 先确认分区存不存在，不存在才建——不能
// 反过来"先建、报 already exists 就忽略"（同其它组件已经踩过的教训）。
func ensurePartition(ctx context.Context, tx *sql.Tx, table string, from, to time.Time) error {
	name := fmt.Sprintf("%s_%s", table, from.Format("2006_01_02"))

	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists); err != nil {
		return fmt.Errorf("检查分区是否存在 %s: %w", name, err)
	}
	if exists {
		return nil
	}

	stmt := fmt.Sprintf(
		`CREATE TABLE %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')`,
		name, table, from.Format("2006-01-02"), to.Format("2006-01-02"),
	)
	if _, err := tx.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("建分区 %s: %w", name, err)
	}
	return nil
}
