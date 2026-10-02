// 五个写命令：CreateOpportunity / UpdateOpportunity / ChangeStage / MarkWon /
// MarkLost。跨组件校验（mdm-customer / mdm-product 的 BatchGet）在 service 层
// 做完，校验结果（客户展示名、产品快照）当参数传进来；这一层只管幂等、状态机
// 与发事件，不发起任何网络调用——事务里夹网络调用，下游一慢就长时间占着行锁。
package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	besdk "github.com/brickKit/be-sdk-go"
)

var ErrVersionConflict = fmt.Errorf("%w: version 与当前值不一致", ErrInvalidArgument)

// runCommand 是五个写命令共用的事务骨架：claim-first 幂等 → 业务步骤 → 落地
// 结果。同一个幂等键第二次进来时直接返回第一次的结果（重放），不再执行 apply。
// apply 返回命令产生 / 影响的那条商机（已经是写完之后的状态）。
//
// 业务步骤与 Outbox 写入都在 apply 里、和状态变更同一个事务：事务提交了事件
// 就一定会发，回滚了事件就一定不发，不存在"商机赢了、事件丢了"的窗口。
func (r *Repo) runCommand(ctx context.Context, key, command string,
	apply func(tx *sql.Tx) (*Opportunity, error)) (*Opportunity, error) {
	if key == "" {
		return nil, fmt.Errorf("%w: idempotency_key 不能为空", ErrInvalidArgument)
	}
	var opp *Opportunity
	err := besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		claimed, err := claimIdempotency(ctx, tx, key, command)
		if err != nil {
			return err
		}
		if !claimed {
			resultID, err := lookupIdempotencyResult(ctx, tx, key)
			if err != nil {
				return err
			}
			opp, err = getOpportunityTx(ctx, tx, resultID)
			return err
		}
		opp, err = apply(tx)
		if err != nil {
			return err
		}
		return finalizeIdempotency(ctx, tx, key, opp.ID)
	})
	if err != nil {
		return nil, err
	}
	return opp, nil
}

// lockedOpportunity 是写命令在 FOR UPDATE 锁住商机之后要用的几个字段。
type lockedOpportunity struct {
	Status  string
	Version int64
	StageID int64
}

// lockOpenTx 锁住商机并确认它还是 OPEN：改阶段、改内容、赢单、输单都只对
// OPEN 有意义。FOR UPDATE 让两个几乎同时到达的命令串行，后到的那个看到的是
// 前一个提交后的状态。
func lockOpenTx(ctx context.Context, tx *sql.Tx, oppID int64, id string) (lockedOpportunity, error) {
	var l lockedOpportunity
	if err := tx.QueryRowContext(ctx,
		`SELECT status, version, stage_id FROM opportunities WHERE id = $1 FOR UPDATE`, oppID,
	).Scan(&l.Status, &l.Version, &l.StageID); err != nil {
		if err == sql.ErrNoRows {
			return l, fmt.Errorf("%w: opportunity id=%s", ErrNotFound, id)
		}
		return l, fmt.Errorf("查 opportunities: %w", err)
	}
	if l.Status != StatusOpen {
		return l, fmt.Errorf("%w: opportunity id=%s status=%s", ErrOpportunityTerminal, id, l.Status)
	}
	return l, nil
}

// publishTx 把事件写进 Outbox（与调用方的状态变更同一个事务），信封的
// version 取商机写完之后的 version，消费方据此丢弃乱序到达的旧事件。
func (r *Repo) publishTx(tx *sql.Tx, subject string, opp *Opportunity, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return besdk.PublishOutbox(tx, r.schema, besdk.Event{
		Subject: subject, AggregateID: opp.ID, Version: opp.Version, Payload: raw,
	})
}

func nullableDate(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// ── CreateOpportunity：建档。起始阶段固定是 sort_order 最小的那个（契约没有
// 起始阶段入参：建档即进漏斗第一格）──

type CreateOpportunityItemInput struct {
	ProductID       string
	ProductSKU      string
	ProductName     string
	UOMID           string
	Qty             string
	QuotedUnitPrice string
}

type CreateOpportunityInput struct {
	IdempotencyKey      string
	Name                string
	CustomerID          string
	CustomerName        string // 快照，调用方从 mdm-customer BatchGet 结果里取
	Items               []CreateOpportunityItemInput
	ExpectedAmount      string
	ProbabilityOverride *int // nil = 用起始阶段的 default_probability
	ExpectedCloseDate   time.Time
	Currency            string
	// dept_id / dept_path / owner_id 是创建时快照，调用方从 besdk.ScopeOf(ctx)
	// 取真实值；之后这个人换部门，商机的归属不跟着变。
	DeptID   string
	DeptPath string
	OwnerID  string
}

func computeSubtotal(qty, unitPrice string) (string, error) {
	q, err := strconv.ParseFloat(qty, 64)
	if err != nil {
		return "", fmt.Errorf("%w: qty 不是合法数字：%q", ErrInvalidArgument, qty)
	}
	p, err := strconv.ParseFloat(unitPrice, 64)
	if err != nil {
		return "", fmt.Errorf("%w: quoted_unit_price 不是合法数字：%q", ErrInvalidArgument, unitPrice)
	}
	return strconv.FormatFloat(q*p, 'f', 2, 64), nil
}

func (r *Repo) CreateOpportunity(ctx context.Context, in CreateOpportunityInput) (*Opportunity, error) {
	if in.IdempotencyKey == "" {
		return nil, fmt.Errorf("%w: idempotency_key 不能为空", ErrInvalidArgument)
	}
	if in.Name == "" {
		return nil, fmt.Errorf("%w: name 不能为空", ErrInvalidArgument)
	}
	if in.CustomerID == "" {
		return nil, fmt.Errorf("%w: customer_id 不能为空", ErrInvalidArgument)
	}
	if len(in.Items) == 0 {
		return nil, fmt.Errorf("%w: items 不能为空", ErrInvalidArgument)
	}

	return r.runCommand(ctx, in.IdempotencyKey, "crm.opportunity.create", func(tx *sql.Tx) (*Opportunity, error) {
		stage, err := firstStageTx(ctx, tx)
		if err != nil {
			return nil, err
		}
		probability := stage.DefaultProbability
		if in.ProbabilityOverride != nil {
			probability = *in.ProbabilityOverride
		}
		stageID, err := parseID("stage_id", stage.ID)
		if err != nil {
			return nil, err
		}

		var id int64
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO opportunities
				(name, customer_id, customer_name, owner_id, dept_id, dept_path,
				 stage_id, expected_amount, probability, expected_close_date, status, currency)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			RETURNING id`,
			in.Name, in.CustomerID, in.CustomerName, in.OwnerID, in.DeptID, in.DeptPath,
			stageID, in.ExpectedAmount, probability, nullableDate(in.ExpectedCloseDate), StatusOpen,
			normalizeCurrency(in.Currency),
		).Scan(&id); err != nil {
			return nil, fmt.Errorf("写 opportunities: %w", err)
		}

		for _, it := range in.Items {
			subtotal, err := computeSubtotal(it.Qty, it.QuotedUnitPrice)
			if err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO opportunity_items
					(opportunity_id, product_id, product_sku, product_name, uom_id, qty, quoted_unit_price, subtotal)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				id, it.ProductID, it.ProductSKU, it.ProductName, it.UOMID, it.Qty, it.QuotedUnitPrice, subtotal,
			); err != nil {
				return nil, fmt.Errorf("写 opportunity_items product_id=%s: %w", it.ProductID, err)
			}
		}
		return getOpportunityTx(ctx, tx, strconv.FormatInt(id, 10))
	})
}

// ── UpdateOpportunity：改名 / 金额 / 预计成交日 / 负责人。全量替换语义，不是
// 部分 PATCH：没变的字段由调用方原样带回。只有 OPEN 能改，version 是乐观锁 ──

type UpdateOpportunityInput struct {
	IdempotencyKey    string
	ID                string
	Version           int64
	Name              string
	ExpectedAmount    string
	ExpectedCloseDate time.Time
	OwnerID           string
}

func (r *Repo) UpdateOpportunity(ctx context.Context, in UpdateOpportunityInput) (*Opportunity, error) {
	if in.IdempotencyKey == "" {
		return nil, fmt.Errorf("%w: idempotency_key 不能为空", ErrInvalidArgument)
	}
	oppID, err := parseID("id", in.ID)
	if err != nil {
		return nil, err
	}

	return r.runCommand(ctx, in.IdempotencyKey, "crm.opportunity.update", func(tx *sql.Tx) (*Opportunity, error) {
		locked, err := lockOpenTx(ctx, tx, oppID, in.ID)
		if err != nil {
			return nil, err
		}
		if locked.Version != in.Version {
			return nil, fmt.Errorf("%w: opportunity id=%s 传入 version=%d 库中 version=%d",
				ErrVersionConflict, in.ID, in.Version, locked.Version)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE opportunities
			SET name = $1, expected_amount = $2, expected_close_date = $3, owner_id = $4,
				version = version + 1, updated_at = now()
			WHERE id = $5`,
			in.Name, in.ExpectedAmount, nullableDate(in.ExpectedCloseDate), in.OwnerID, oppID,
		); err != nil {
			return nil, fmt.Errorf("更新 opportunities: %w", err)
		}
		return getOpportunityTx(ctx, tx, in.ID)
	})
}

// ── ChangeStage：阶段推进，写历史。任意两个阶段之间都可以跳转，只记历史，
// 不做"必须按顺序推进""跳阶段要审批"这类约束（这是客户之间真实存在的分歧，
// 标准版取最宽松的一种，更严的流程管控走定制）──

type ChangeStageInput struct {
	IdempotencyKey      string
	ID                  string
	ToStageID           string
	ProbabilityOverride *int // nil = 用目标阶段的 default_probability
	ChangedBy           string
}

func (r *Repo) ChangeStage(ctx context.Context, in ChangeStageInput) (*Opportunity, error) {
	if in.IdempotencyKey == "" {
		return nil, fmt.Errorf("%w: idempotency_key 不能为空", ErrInvalidArgument)
	}
	oppID, err := parseID("id", in.ID)
	if err != nil {
		return nil, err
	}
	toStageID, err := parseID("to_stage_id", in.ToStageID)
	if err != nil {
		return nil, err
	}

	return r.runCommand(ctx, in.IdempotencyKey, "crm.opportunity.change_stage", func(tx *sql.Tx) (*Opportunity, error) {
		locked, err := lockOpenTx(ctx, tx, oppID, in.ID)
		if err != nil {
			return nil, err
		}
		toStage, err := getStageTx(ctx, tx, toStageID)
		if err != nil {
			return nil, err
		}
		probability := toStage.DefaultProbability
		if in.ProbabilityOverride != nil {
			probability = *in.ProbabilityOverride
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE opportunities SET stage_id = $1, probability = $2, version = version + 1, updated_at = now()
			WHERE id = $3`, toStageID, probability, oppID); err != nil {
			return nil, fmt.Errorf("更新 opportunities: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO opportunity_stage_history (opportunity_id, from_stage_id, to_stage_id, changed_by)
			VALUES ($1, $2, $3, $4)`, oppID, locked.StageID, toStageID, in.ChangedBy); err != nil {
			return nil, fmt.Errorf("写 opportunity_stage_history: %w", err)
		}

		opp, err := getOpportunityTx(ctx, tx, in.ID)
		if err != nil {
			return nil, err
		}
		return opp, r.publishTx(tx, "crm.opportunity.stage_changed.v1", opp, map[string]any{
			"opportunity_id": in.ID,
			"from_stage":     strconv.FormatInt(locked.StageID, 10),
			"to_stage":       in.ToStageID,
		})
	})
}

// ── MarkWon：赢单，链路的扳机。转 WON + 写 Outbox 在同一个事务里。事件必须
// 带 owner_id / dept_path：erp-sales 在事件消费路径上建单，那里没有用户的
// JWT，订单的归属只能从事件里来——漏了不报错，订单建出来了，只是"我的订单"
// 和部门视图里谁都看不到它 ──

type MarkWonInput struct {
	IdempotencyKey string
	ID             string
}

func (r *Repo) MarkWon(ctx context.Context, in MarkWonInput) (*Opportunity, error) {
	if in.IdempotencyKey == "" {
		return nil, fmt.Errorf("%w: idempotency_key 不能为空", ErrInvalidArgument)
	}
	oppID, err := parseID("id", in.ID)
	if err != nil {
		return nil, err
	}

	return r.runCommand(ctx, in.IdempotencyKey, "crm.opportunity.win", func(tx *sql.Tx) (*Opportunity, error) {
		if _, err := lockOpenTx(ctx, tx, oppID, in.ID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE opportunities SET status = $1, probability = 100, won_at = now(), version = version + 1, updated_at = now()
			WHERE id = $2`, StatusWon, oppID); err != nil {
			return nil, fmt.Errorf("更新 opportunities: %w", err)
		}
		opp, err := getOpportunityTx(ctx, tx, in.ID)
		if err != nil {
			return nil, err
		}
		return opp, r.publishTx(tx, "crm.opportunity.won.v1", opp, wonPayload(opp))
	})
}

func wonPayload(o *Opportunity) map[string]any {
	items := make([]map[string]string, 0, len(o.Items))
	for _, it := range o.Items {
		items = append(items, map[string]string{
			"product_id": it.ProductID, "quantity": it.Qty, "quoted_unit_price": it.QuotedUnitPrice,
		})
	}
	payload := map[string]any{
		"opportunity_id": o.ID, "customer_id": o.CustomerID, "items": items,
		"owner_id": o.OwnerID, "dept_path": o.DeptPath, "currency": o.Currency,
		"revision": o.Version,
	}
	if !o.ExpectedCloseDate.IsZero() {
		payload["expected_close_date"] = o.ExpectedCloseDate.Format(time.RFC3339)
	}
	return payload
}

// ── MarkLost：输单，必须填原因（失单原因是销售复盘最有用的那一列）──

type MarkLostInput struct {
	IdempotencyKey string
	ID             string
	Reason         string
}

func (r *Repo) MarkLost(ctx context.Context, in MarkLostInput) (*Opportunity, error) {
	if in.IdempotencyKey == "" {
		return nil, fmt.Errorf("%w: idempotency_key 不能为空", ErrInvalidArgument)
	}
	if in.Reason == "" {
		return nil, fmt.Errorf("%w: reason 不能为空", ErrInvalidArgument)
	}
	oppID, err := parseID("id", in.ID)
	if err != nil {
		return nil, err
	}

	return r.runCommand(ctx, in.IdempotencyKey, "crm.opportunity.lose", func(tx *sql.Tx) (*Opportunity, error) {
		if _, err := lockOpenTx(ctx, tx, oppID, in.ID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE opportunities SET status = $1, lost_reason = $2, version = version + 1, updated_at = now()
			WHERE id = $3`, StatusLost, in.Reason, oppID); err != nil {
			return nil, fmt.Errorf("更新 opportunities: %w", err)
		}
		opp, err := getOpportunityTx(ctx, tx, in.ID)
		if err != nil {
			return nil, err
		}
		return opp, r.publishTx(tx, "crm.opportunity.lost.v1", opp, map[string]any{
			"opportunity_id": in.ID, "reason": in.Reason, "amount": opp.ExpectedAmount,
		})
	})
}
