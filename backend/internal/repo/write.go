// 五个写命令：CreateOpportunity/UpdateOpportunity/ChangeStage/MarkWon/
// MarkLost。⚠️ 本组件没有 TCC 编排——两条强依赖（mdm-customer/
// mdm-product 的 BatchGet）在 service 层直接校验完、把校验结果（客户
// 展示名、产品快照）当参数传进来，这一层只管幂等 + 状态机 + 发事件，
// 不发起任何网络调用（同 erp-sales FinalizeConfirm 的既有判据，但没有
// 补偿链）。
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

// ── CreateOpportunity：建档，起始阶段固定用 sort_order 最小的那个
// （契约没有留 stage_id 入参）。跨组件校验（BatchGet 客户/产品）由
// service 层做完，这里只管落库 ──

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
	// dept_id/dept_path/owner_id 是创建时快照（设计计划 §1）。调用方从
	// besdk.ScopeOf(ctx) 取真实值。
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

	var opp *Opportunity
	err := besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		claimed, err := claimIdempotency(ctx, tx, in.IdempotencyKey, "crm.opportunity.create")
		if err != nil {
			return err
		}
		if !claimed {
			resultID, err := lookupIdempotencyResult(ctx, tx, in.IdempotencyKey)
			if err != nil {
				return err
			}
			opp, err = getOpportunityTx(ctx, tx, resultID)
			return err
		}

		stage, err := firstStageTx(ctx, tx)
		if err != nil {
			return err
		}
		probability := stage.DefaultProbability
		if in.ProbabilityOverride != nil {
			probability = *in.ProbabilityOverride
		}
		stageID, err := parseID("stage_id", stage.ID)
		if err != nil {
			return err
		}

		var expectedCloseDate any
		if !in.ExpectedCloseDate.IsZero() {
			expectedCloseDate = in.ExpectedCloseDate
		}

		var id int64
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO opportunities
				(name, customer_id, customer_name, owner_id, dept_id, dept_path,
				 stage_id, expected_amount, probability, expected_close_date, status, currency)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			RETURNING id`,
			in.Name, in.CustomerID, in.CustomerName, in.OwnerID, in.DeptID, in.DeptPath,
			stageID, in.ExpectedAmount, probability, expectedCloseDate, StatusOpen, normalizeCurrency(in.Currency),
		).Scan(&id); err != nil {
			return fmt.Errorf("写 opportunities: %w", err)
		}

		for _, it := range in.Items {
			subtotal, err := computeSubtotal(it.Qty, it.QuotedUnitPrice)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO opportunity_items
					(opportunity_id, product_id, product_sku, product_name, uom_id, qty, quoted_unit_price, subtotal)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				id, it.ProductID, it.ProductSKU, it.ProductName, it.UOMID, it.Qty, it.QuotedUnitPrice, subtotal,
			); err != nil {
				return fmt.Errorf("写 opportunity_items product_id=%s: %w", it.ProductID, err)
			}
		}

		idStr := strconv.FormatInt(id, 10)
		if err := finalizeIdempotency(ctx, tx, in.IdempotencyKey, idStr); err != nil {
			return err
		}
		opp, err = getOpportunityTx(ctx, tx, idStr)
		return err
	})
	if err != nil {
		return nil, err
	}
	return opp, nil
}

// ── UpdateOpportunity：改名/金额/预计成交日/负责人。全量替换语义（同
// mdm-customer UpdateRequest 的既有判据），不是部分 PATCH——未变的字段
// 由调用方原样带回。只有 OPEN 状态能改（设计计划没有给"改已终结商机"
// 留任何语义）──

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

	var opp *Opportunity
	err = besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		claimed, err := claimIdempotency(ctx, tx, in.IdempotencyKey, "crm.opportunity.update")
		if err != nil {
			return err
		}
		if !claimed {
			resultID, err := lookupIdempotencyResult(ctx, tx, in.IdempotencyKey)
			if err != nil {
				return err
			}
			opp, err = getOpportunityTx(ctx, tx, resultID)
			return err
		}

		var status string
		var version int64
		if err := tx.QueryRowContext(ctx,
			`SELECT status, version FROM opportunities WHERE id = $1 FOR UPDATE`, oppID,
		).Scan(&status, &version); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("%w: opportunity id=%s", ErrNotFound, in.ID)
			}
			return fmt.Errorf("查 opportunities: %w", err)
		}
		if status != StatusOpen {
			return fmt.Errorf("%w: opportunity id=%s status=%s", ErrOpportunityTerminal, in.ID, status)
		}
		if version != in.Version {
			return fmt.Errorf("%w: opportunity id=%s 传入 version=%d 库中 version=%d", ErrVersionConflict, in.ID, in.Version, version)
		}

		var expectedCloseDate any
		if !in.ExpectedCloseDate.IsZero() {
			expectedCloseDate = in.ExpectedCloseDate
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE opportunities
			SET name = $1, expected_amount = $2, expected_close_date = $3, owner_id = $4,
				version = version + 1, updated_at = now()
			WHERE id = $5`,
			in.Name, in.ExpectedAmount, expectedCloseDate, in.OwnerID, oppID,
		); err != nil {
			return fmt.Errorf("更新 opportunities: %w", err)
		}

		opp, err = getOpportunityTx(ctx, tx, in.ID)
		if err != nil {
			return err
		}
		return finalizeIdempotency(ctx, tx, in.IdempotencyKey, in.ID)
	})
	if err != nil {
		return nil, err
	}
	return opp, nil
}

// ── ChangeStage：阶段推进，写历史。本阶段任意阶段之间都可以跳转，只记
// 历史，不实现"必须按顺序推进""跳阶段要审批"这类约束（设计计划 §2.2、
// §8 的 R-4 判定：这两条分歧判定为 Fork 点，不建族）──

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

	var opp *Opportunity
	err = besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		claimed, err := claimIdempotency(ctx, tx, in.IdempotencyKey, "crm.opportunity.change_stage")
		if err != nil {
			return err
		}
		if !claimed {
			resultID, err := lookupIdempotencyResult(ctx, tx, in.IdempotencyKey)
			if err != nil {
				return err
			}
			opp, err = getOpportunityTx(ctx, tx, resultID)
			return err
		}

		var status string
		var fromStageID int64
		if err := tx.QueryRowContext(ctx,
			`SELECT status, stage_id FROM opportunities WHERE id = $1 FOR UPDATE`, oppID,
		).Scan(&status, &fromStageID); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("%w: opportunity id=%s", ErrNotFound, in.ID)
			}
			return fmt.Errorf("查 opportunities: %w", err)
		}
		if status != StatusOpen {
			return fmt.Errorf("%w: opportunity id=%s status=%s", ErrOpportunityTerminal, in.ID, status)
		}

		toStage, err := getStageTx(ctx, tx, toStageID)
		if err != nil {
			return err
		}
		probability := toStage.DefaultProbability
		if in.ProbabilityOverride != nil {
			probability = *in.ProbabilityOverride
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE opportunities SET stage_id = $1, probability = $2, version = version + 1, updated_at = now()
			WHERE id = $3`, toStageID, probability, oppID); err != nil {
			return fmt.Errorf("更新 opportunities: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO opportunity_stage_history (opportunity_id, from_stage_id, to_stage_id, changed_by)
			VALUES ($1, $2, $3, $4)`, oppID, fromStageID, toStageID, in.ChangedBy); err != nil {
			return fmt.Errorf("写 opportunity_stage_history: %w", err)
		}

		opp, err = getOpportunityTx(ctx, tx, in.ID)
		if err != nil {
			return err
		}

		payload, err := json.Marshal(map[string]any{
			"opportunity_id": in.ID,
			"from_stage":     strconv.FormatInt(fromStageID, 10),
			"to_stage":       in.ToStageID,
		})
		if err != nil {
			return err
		}
		if err := besdk.PublishOutbox(tx, r.schema, besdk.Event{
			Subject: "crm.opportunity.stage_changed.v1", AggregateID: in.ID, Version: opp.Version, Payload: payload,
		}); err != nil {
			return err
		}

		return finalizeIdempotency(ctx, tx, in.IdempotencyKey, in.ID)
	})
	if err != nil {
		return nil, err
	}
	return opp, nil
}

// ── MarkWon：⭐ 链路的扳机（设计计划 §4.1）。转 WON + 写 Outbox，全部在
// 同一个事务里。⚠️ owner_id/dept_path 必须带进事件——erp-sales 的
// handler 跑在事件消费路径（SystemClient/无 JWT 上下文），漏了这两个
// 字段订单会建出来但没有归属，且不报任何错 ──

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

	var opp *Opportunity
	err = besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		claimed, err := claimIdempotency(ctx, tx, in.IdempotencyKey, "crm.opportunity.win")
		if err != nil {
			return err
		}
		if !claimed {
			resultID, err := lookupIdempotencyResult(ctx, tx, in.IdempotencyKey)
			if err != nil {
				return err
			}
			opp, err = getOpportunityTx(ctx, tx, resultID)
			return err
		}

		var status string
		if err := tx.QueryRowContext(ctx,
			`SELECT status FROM opportunities WHERE id = $1 FOR UPDATE`, oppID,
		).Scan(&status); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("%w: opportunity id=%s", ErrNotFound, in.ID)
			}
			return fmt.Errorf("查 opportunities: %w", err)
		}
		if status != StatusOpen {
			return fmt.Errorf("%w: opportunity id=%s status=%s", ErrOpportunityTerminal, in.ID, status)
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE opportunities SET status = $1, probability = 100, won_at = now(), version = version + 1, updated_at = now()
			WHERE id = $2`, StatusWon, oppID); err != nil {
			return fmt.Errorf("更新 opportunities: %w", err)
		}

		opp, err = getOpportunityTx(ctx, tx, in.ID)
		if err != nil {
			return err
		}

		payload, err := buildWonPayload(opp)
		if err != nil {
			return err
		}
		if err := besdk.PublishOutbox(tx, r.schema, besdk.Event{
			Subject: "crm.opportunity.won.v1", AggregateID: in.ID, Version: opp.Version, Payload: payload,
		}); err != nil {
			return err
		}

		return finalizeIdempotency(ctx, tx, in.IdempotencyKey, in.ID)
	})
	if err != nil {
		return nil, err
	}
	return opp, nil
}

func buildWonPayload(o *Opportunity) ([]byte, error) {
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
	return json.Marshal(payload)
}

// ── MarkLost：转 LOST，要求填原因（设计计划 §8，借鉴 Odoo loss reason）──

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

	var opp *Opportunity
	err = besdk.WithTx(ctx, r.db, r.role, r.schema, func(tx *sql.Tx) error {
		claimed, err := claimIdempotency(ctx, tx, in.IdempotencyKey, "crm.opportunity.lose")
		if err != nil {
			return err
		}
		if !claimed {
			resultID, err := lookupIdempotencyResult(ctx, tx, in.IdempotencyKey)
			if err != nil {
				return err
			}
			opp, err = getOpportunityTx(ctx, tx, resultID)
			return err
		}

		var status string
		if err := tx.QueryRowContext(ctx,
			`SELECT status FROM opportunities WHERE id = $1 FOR UPDATE`, oppID,
		).Scan(&status); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("%w: opportunity id=%s", ErrNotFound, in.ID)
			}
			return fmt.Errorf("查 opportunities: %w", err)
		}
		if status != StatusOpen {
			return fmt.Errorf("%w: opportunity id=%s status=%s", ErrOpportunityTerminal, in.ID, status)
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE opportunities SET status = $1, lost_reason = $2, version = version + 1, updated_at = now()
			WHERE id = $3`, StatusLost, in.Reason, oppID); err != nil {
			return fmt.Errorf("更新 opportunities: %w", err)
		}

		opp, err = getOpportunityTx(ctx, tx, in.ID)
		if err != nil {
			return err
		}

		payload, err := json.Marshal(map[string]any{
			"opportunity_id": in.ID, "reason": in.Reason, "amount": opp.ExpectedAmount,
		})
		if err != nil {
			return err
		}
		if err := besdk.PublishOutbox(tx, r.schema, besdk.Event{
			Subject: "crm.opportunity.lost.v1", AggregateID: in.ID, Version: opp.Version, Payload: payload,
		}); err != nil {
			return err
		}

		return finalizeIdempotency(ctx, tx, in.IdempotencyKey, in.ID)
	})
	if err != nil {
		return nil, err
	}
	return opp, nil
}
