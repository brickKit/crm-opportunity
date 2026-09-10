package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	besdk "github.com/brickKit/be-sdk-go"
)

const (
	StatusOpen      = "OPEN"
	StatusWon       = "WON"
	StatusLost      = "LOST"
	StatusCancelled = "CANCELLED"
)

// OpportunityItem 是 opportunity_items 一行的视图。**全部快照，不 join**
// （设计计划 §2.1）。
type OpportunityItem struct {
	ProductID       string
	ProductSKU      string
	ProductName     string
	UOMID           string
	Qty             string
	QuotedUnitPrice string
	Subtotal        string
}

// Opportunity 是 opportunities 一行 + 其行的视图 + 关联阶段名（读时 join，
// 不落库）。dept_id/dept_path/owner_id 是创建时快照（设计计划 §1）。
type Opportunity struct {
	ID                string
	Name              string
	CustomerID        string
	CustomerName      string
	OwnerID           string
	DeptID            string
	DeptPath          string
	StageID           string
	StageName         string
	Items             []OpportunityItem
	ExpectedAmount    string
	Probability       int
	WeightedAmount    string // ⭐ = ExpectedAmount × Probability，算出来不存（设计计划 §2）
	ExpectedCloseDate time.Time
	Status            string
	Currency          string
	LostReason        string
	WonAt             time.Time
	Version           int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func computeWeightedAmount(expectedAmount string, probability int) string {
	amount, err := strconv.ParseFloat(expectedAmount, 64)
	if err != nil {
		return "0.00"
	}
	return strconv.FormatFloat(amount*float64(probability)/100, 'f', 2, 64)
}

func getOpportunityTx(ctx context.Context, tx *sql.Tx, id string) (*Opportunity, error) {
	oppID, err := parseID("id", id)
	if err != nil {
		return nil, err
	}
	var o Opportunity
	var rawID, stageID int64
	var expectedCloseDate, wonAt sql.NullTime
	row := tx.QueryRowContext(ctx, `
		SELECT o.id, o.name, o.customer_id, o.customer_name, o.owner_id, o.dept_id, o.dept_path,
			o.stage_id, s.name, o.expected_amount, o.probability, o.expected_close_date, o.status,
			o.currency, o.lost_reason, o.won_at, o.version, o.created_at, o.updated_at
		FROM opportunities o JOIN opportunity_stages s ON s.id = o.stage_id
		WHERE o.id = $1`, oppID)
	if err := row.Scan(&rawID, &o.Name, &o.CustomerID, &o.CustomerName, &o.OwnerID, &o.DeptID, &o.DeptPath,
		&stageID, &o.StageName, &o.ExpectedAmount, &o.Probability, &expectedCloseDate, &o.Status,
		&o.Currency, &o.LostReason, &wonAt, &o.Version, &o.CreatedAt, &o.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("%w: opportunity id=%s", ErrNotFound, id)
		}
		return nil, fmt.Errorf("查 opportunities: %w", err)
	}
	o.ID = strconv.FormatInt(rawID, 10)
	o.StageID = strconv.FormatInt(stageID, 10)
	o.WeightedAmount = computeWeightedAmount(o.ExpectedAmount, o.Probability)
	if expectedCloseDate.Valid {
		o.ExpectedCloseDate = expectedCloseDate.Time
	}
	if wonAt.Valid {
		o.WonAt = wonAt.Time
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT product_id, product_sku, product_name, uom_id, qty, quoted_unit_price, subtotal
		FROM opportunity_items WHERE opportunity_id = $1 ORDER BY id`, oppID)
	if err != nil {
		return nil, fmt.Errorf("查 opportunity_items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var it OpportunityItem
		if err := rows.Scan(&it.ProductID, &it.ProductSKU, &it.ProductName, &it.UOMID,
			&it.Qty, &it.QuotedUnitPrice, &it.Subtotal); err != nil {
			return nil, err
		}
		o.Items = append(o.Items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &o, nil
}

func (r *Repo) GetOpportunity(ctx context.Context, id string) (*Opportunity, error) {
	var o *Opportunity
	err := besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		var err error
		o, err = getOpportunityTx(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return o, nil
}

// BatchGetOpportunities 是防 N+1 的唯一合法调用方式（§3.8）。只返回找到的。
func (r *Repo) BatchGetOpportunities(ctx context.Context, ids []string) (found []*Opportunity, missing []string, err error) {
	for _, id := range ids {
		o, err := r.GetOpportunity(ctx, id)
		if err != nil {
			if err == ErrNotFound {
				missing = append(missing, id)
				continue
			}
			return nil, nil, err
		}
		found = append(found, o)
	}
	return found, missing, nil
}

// ListInput 对应 ListOpportunitiesRequest。刻意没有 offset 字段（决策 53）。
type ListInput struct {
	Cursor        string
	PageSize      int
	CustomerID    string
	StatusFilter  string
	StageIDFilter string
	CreatedAfter  time.Time
	CreatedBefore time.Time
	// ── org/owner 两维数据范围，"我的商机"和"本部门及下级的商机"是
	// 销售组织最基本的两个视图（设计计划 §1），同 erp-sales ListInput
	// 的既有判据：由调用方（listOpportunitiesHandler 的 ?view= 参数）
	// 静态选择用哪一个。
	ViewMine    bool
	ScopePrefix string
	ScopeOwner  string
}

type ListResult struct {
	Opportunities []*Opportunity
	NextCursor    string
}

func (r *Repo) ListOpportunities(ctx context.Context, in ListInput) (*ListResult, error) {
	q := besdk.ListWindow(besdk.Query{
		From: in.CreatedAfter, To: in.CreatedBefore, Cursor: in.Cursor, Limit: in.PageSize,
	})
	var ck *cursorKey
	if q.Cursor != "" {
		decoded, err := decodeCursor(q.Cursor)
		if err != nil {
			return nil, fmt.Errorf("非法 cursor：%w", err)
		}
		ck = &decoded
	}

	var out ListResult
	err := besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		query := `SELECT id FROM opportunities WHERE created_at >= $1 AND created_at <= $2`
		args := []any{q.From, q.To}
		if in.ViewMine {
			args = append(args, in.ScopeOwner)
			query += fmt.Sprintf(" AND owner_id = $%d", len(args))
		} else {
			// ScopePrefix 为空（部门树根节点）时 `LIKE '' || '%'` 等价于
			// `LIKE '%'`，天然匹配全部，不需要特判（§14.2.4 的既有判据）。
			args = append(args, in.ScopePrefix)
			query += fmt.Sprintf(" AND dept_path LIKE $%d || '%%'", len(args))
		}
		if in.CustomerID != "" {
			args = append(args, in.CustomerID)
			query += fmt.Sprintf(" AND customer_id = $%d", len(args))
		}
		if in.StatusFilter != "" {
			args = append(args, in.StatusFilter)
			query += fmt.Sprintf(" AND status = $%d", len(args))
		}
		if in.StageIDFilter != "" {
			stageID, err := parseID("stage_id_filter", in.StageIDFilter)
			if err != nil {
				return err
			}
			args = append(args, stageID)
			query += fmt.Sprintf(" AND stage_id = $%d", len(args))
		}
		if ck != nil {
			args = append(args, ck.CreatedAt, ck.ID)
			query += fmt.Sprintf(" AND (created_at, id) < ($%d, $%d)", len(args)-1, len(args))
		}
		args = append(args, q.Limit+1)
		query += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", len(args))

		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("查 opportunities: %w", err)
		}
		var ids []int64
		for rows.Next() {
			var rawID int64
			if err := rows.Scan(&rawID); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, rawID)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		// ⚠️ N+1：每个 id 再查一次头 + 行 + 阶段名。List 的默认页大小与
		// 强制时间窗口（besdk.ListWindow）把 N 卡在合理范围内（同
		// erp-sales ListOrders 的既有判据）。
		opps := make([]*Opportunity, 0, len(ids))
		for _, rawID := range ids {
			o, err := getOpportunityTx(ctx, tx, strconv.FormatInt(rawID, 10))
			if err != nil {
				return err
			}
			opps = append(opps, o)
		}

		if len(opps) > q.Limit {
			last := opps[q.Limit-1]
			lastRawID, err := strconv.ParseInt(last.ID, 10, 64)
			if err != nil {
				return err
			}
			out.NextCursor = encodeCursor(cursorKey{CreatedAt: last.CreatedAt, ID: lastRawID})
			opps = opps[:q.Limit]
		}
		out.Opportunities = opps
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// normalizeCurrency 留空则按本位币默认值处理（标准版单币种，设计计划 §4.1）。
func normalizeCurrency(c string) string {
	if strings.TrimSpace(c) == "" {
		return "CNY"
	}
	return c
}
