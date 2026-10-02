package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	besdk "github.com/brickKit/be-sdk-go"
)

// StageHistoryEntry 是 opportunity_stage_history 的一行。Probability 为 nil 表示
// 这一行写下时还没有记录概率。
type StageHistoryEntry struct {
	FromStageID string
	ToStageID   string
	Probability *int
	ChangedBy   string
	ChangedAt   time.Time
}

// ListStageHistory 按时间先后返回一个商机的全部阶段流转。不分页：这是一个
// 商机的子集合，条数受"一个商机换了几次阶段"限制。调用方先确认商机存在且在
// 数据范围内。
func (r *Repo) ListStageHistory(ctx context.Context, opportunityID string) ([]StageHistoryEntry, error) {
	oppID, err := parseID("id", opportunityID)
	if err != nil {
		return nil, err
	}
	var out []StageHistoryEntry
	err = besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT from_stage_id, to_stage_id, probability, changed_by, created_at
			FROM opportunity_stage_history WHERE opportunity_id = $1
			ORDER BY created_at, id`, oppID)
		if err != nil {
			return fmt.Errorf("查 opportunity_stage_history: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var e StageHistoryEntry
			var from sql.NullInt64
			var to int64
			var probability sql.NullInt32
			if err := rows.Scan(&from, &to, &probability, &e.ChangedBy, &e.ChangedAt); err != nil {
				return err
			}
			if from.Valid {
				e.FromStageID = strconv.FormatInt(from.Int64, 10)
			}
			e.ToStageID = strconv.FormatInt(to, 10)
			if probability.Valid {
				p := int(probability.Int32)
				e.Probability = &p
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
