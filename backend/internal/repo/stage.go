package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	besdk "github.com/brickKit/be-sdk-go"
)

// Stage 是 opportunity_stages 一行的视图。阶段是数据不是代码（设计计划
// §2.2），迁移播种，本组件不提供写接口——改阶段清单是运维操作，不是
// 业务操作。
type Stage struct {
	ID                 string
	Code               string
	Name               string
	SortOrder          int
	DefaultProbability int
	IsFinal            bool
}

func getStageTx(ctx context.Context, tx *sql.Tx, id int64) (*Stage, error) {
	var s Stage
	var rawID int64
	err := tx.QueryRowContext(ctx, `
		SELECT id, code, name, sort_order, default_probability, is_final
		FROM opportunity_stages WHERE id = $1`, id,
	).Scan(&rawID, &s.Code, &s.Name, &s.SortOrder, &s.DefaultProbability, &s.IsFinal)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: stage id=%d", ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("查 opportunity_stages: %w", err)
	}
	s.ID = strconv.FormatInt(rawID, 10)
	return &s, nil
}

// firstStageTx 返回 sort_order 最小的阶段——CreateOpportunity 建档时的
// 默认起始阶段（契约没有留 stage_id 入参，设计计划 §3 的 CreateOpportunity
// 描述只提"建档，校验客户与产品"，没有提"指定起始阶段"）。
func firstStageTx(ctx context.Context, tx *sql.Tx) (*Stage, error) {
	var id int64
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM opportunity_stages ORDER BY sort_order ASC LIMIT 1`,
	).Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w: 尚未播种任何阶段", ErrNotFound)
		}
		return nil, fmt.Errorf("查 opportunity_stages: %w", err)
	}
	return getStageTx(ctx, tx, id)
}

// ListStages 供前端阶段选择器/漏斗视图读，按 sort_order 升序。
func (r *Repo) ListStages(ctx context.Context) ([]*Stage, error) {
	var out []*Stage
	err := besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, code, name, sort_order, default_probability, is_final
			FROM opportunity_stages ORDER BY sort_order ASC`)
		if err != nil {
			return fmt.Errorf("查 opportunity_stages: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var s Stage
			var rawID int64
			if err := rows.Scan(&rawID, &s.Code, &s.Name, &s.SortOrder, &s.DefaultProbability, &s.IsFinal); err != nil {
				return err
			}
			s.ID = strconv.FormatInt(rawID, 10)
			out = append(out, &s)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
