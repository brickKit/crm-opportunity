// Package repo 是 crm-opportunity 的数据访问层：商机主体/行、阶段定义、
// 阶段流转历史、客户展示快照。⚠️ 本组件没有 TCC 编排——两条强依赖
// （mdm-customer/mdm-product 的 BatchGet）在 service 层直接校验完就写库，
// 没有跨组件补偿链（设计计划 §5：不像 erp-sales 那样要发起 Reserve 这类
// 需要撤销的远程动作）。
package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ── 哨兵错误。grpc/http 两层通过 service.ToStatus 统一映射（同
// erp-sales/erp-inventory/erp-finance 的判据）──

var ErrInvalidArgument = errors.New("参数不合法")
var ErrNotFound = errors.New("not found")

// ErrOpportunityTerminal：对一个已经是终态（WON/LOST/CANCELLED）的商机
// 做本该只有 OPEN 才能做的操作（改阶段、赢单、输单）。
var ErrOpportunityTerminal = errors.New("商机已经是终态，不能再流转")

// ErrForbidden：调用者对某个具体商机既不在其 org 范围内也不是 owner
// （§14.2.2 的 org+owner 两维）。⚠️ 这个商机真实存在，调用者只是看不见，
// 与 ErrNotFound 语义不同，不能混用。
var ErrForbidden = errors.New("无权访问该商机")

// InScope 判断这个商机是否在调用者的数据范围内——org（dept_path 前缀
// 匹配）或 owner（owner_id 精确匹配）任一命中就算在范围内，同
// erp-sales Order.InScope 的既有判据（"本部门及下级"与"我的商机"是
// 同一个人可能同时具备的两种"看得到"的理由，不是互斥的，用 OR 不是
// AND）。
func (o *Opportunity) InScope(scopePrefix, scopeOwner string) bool {
	return strings.HasPrefix(o.DeptPath, scopePrefix) || o.OwnerID == scopeOwner
}

type Repo struct {
	db     *sql.DB
	role   string
	schema string
}

func New(db *sql.DB, role, schema string) *Repo {
	return &Repo{db: db, role: role, schema: schema}
}

// ── 幂等：claim-first（同 erp-sales/erp-inventory/erp-finance 的判据）──
//
// CreateOpportunity/UpdateOpportunity/ChangeStage/MarkWon/MarkLost 五个
// 写命令都用它：先原子声明（INSERT ... ON CONFLICT DO NOTHING），声明
// 成功才做真正的工作。
func claimIdempotency(ctx context.Context, tx *sql.Tx, key, command string) (claimed bool, err error) {
	res, err := tx.ExecContext(ctx,
		`INSERT INTO command_idempotency (idempotency_key, command, result_id) VALUES ($1, $2, '')
		 ON CONFLICT (idempotency_key) DO NOTHING`,
		key, command)
	if err != nil {
		return false, fmt.Errorf("声明 command_idempotency: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func finalizeIdempotency(ctx context.Context, tx *sql.Tx, key, resultID string) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE command_idempotency SET result_id = $1 WHERE idempotency_key = $2`, resultID, key)
	if err != nil {
		return fmt.Errorf("落地 command_idempotency 结果: %w", err)
	}
	return nil
}

func lookupIdempotencyResult(ctx context.Context, tx *sql.Tx, key string) (string, error) {
	var resultID string
	if err := tx.QueryRowContext(ctx,
		`SELECT result_id FROM command_idempotency WHERE idempotency_key = $1`, key).Scan(&resultID); err != nil {
		return "", fmt.Errorf("查 command_idempotency: %w", err)
	}
	return resultID, nil
}

func parseID(field, s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s 不合法：%q", ErrInvalidArgument, field, s)
	}
	return id, nil
}
