package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	besdk "github.com/brickKit/be-sdk-go"
)

// FunnelStage 是漏斗的一格：某个阶段上 OPEN 商机的条数、预计金额合计、加权
// 金额合计（十进制字符串，两位小数）。
type FunnelStage struct {
	StageID        string
	StageName      string
	Count          int
	ExpectedAmount string
	WeightedAmount string
}

// FunnelInput 的视角与 ListInput 相同：ViewMine 时只按 ScopeOwner，否则只按
// ScopePrefix——是二选一的切换，不是 OR。两个值都由 service 层从调用者的
// token 填，不接受调用方传入。dept 视角下 ScopePrefix 留空报 ErrInvalidArgument。
type FunnelInput struct {
	ViewMine    bool
	ScopePrefix string
	ScopeOwner  string
}

// Funnel 列出每个阶段（按 sort_order，没有商机的阶段是 0）。加权金额是每条
// 商机的加权金额先四舍五入到分再求和，与列表里逐条显示的值相加一致；
// round(numeric, 2) 与 Go 侧 formatCents 同为 .5 远离零。不加时间窗口：漏斗
// 是"现在所有还开着的商机"，不论建了多久。
func (r *Repo) Funnel(ctx context.Context, in FunnelInput) ([]FunnelStage, error) {
	if !in.ViewMine && in.ScopePrefix == "" {
		return nil, errEmptyScopePrefix
	}
	scopeCond, scopeArg := "o.dept_path LIKE $2 || '%'", in.ScopePrefix
	if in.ViewMine {
		scopeCond, scopeArg = "o.owner_id = $2", in.ScopeOwner
	}
	query := fmt.Sprintf(`
		SELECT s.id, s.name, count(o.id),
			round(coalesce(sum(o.expected_amount), 0), 2)::text,
			round(coalesce(sum(round(o.expected_amount * o.probability / 100.0, 2)), 0), 2)::text
		FROM opportunity_stages s
		LEFT JOIN opportunities o ON o.stage_id = s.id AND o.status = $1 AND %s
		GROUP BY s.id, s.name, s.sort_order
		ORDER BY s.sort_order, s.id`, scopeCond)

	var out []FunnelStage
	err := besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, query, StatusOpen, scopeArg)
		if err != nil {
			return fmt.Errorf("统计漏斗: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var st FunnelStage
			var id int64
			if err := rows.Scan(&id, &st.StageName, &st.Count, &st.ExpectedAmount, &st.WeightedAmount); err != nil {
				return err
			}
			st.StageID = strconv.FormatInt(id, 10)
			out = append(out, st)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
