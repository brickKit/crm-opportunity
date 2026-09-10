// 真实 Postgres 集成测试（同 erp-sales/erp-inventory/erp-finance 的既有
// 判据）：TEST_PG_DSN 没设置就跳过，不伪造数据也不用内存里的假实现
// 代替。⚠️ MarkWon 的测试额外真订阅 NATS、真跑 besdk.StartOutboxPump，
// 断言事件真的发布到总线——这是阶段三计划文档对 Task 13 明文要求的
// 验证标准："断言 opportunities 状态变化 + Outbox 记录 + 事件真的发布到
// 总线，不是只测本组件内部状态，事件发布这一步必须真验"。
package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nats-io/nats.go"

	besdk "github.com/brickKit/be-sdk-go"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设置 TEST_PG_DSN")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func natsURLForTest(t *testing.T) string {
	t.Helper()
	if u := os.Getenv("TEST_NATS_URL"); u != "" {
		return u
	}
	return nats.DefaultURL
}

var seq int64

func uniqueSuffix(prefix string) string {
	n := atomic.AddInt64(&seq, 1)
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), n)
}

// createTestOpportunity 建一条真实 OPEN 商机，供后续命令测试用。
func createTestOpportunity(t *testing.T, ctx context.Context, r *Repo) *Opportunity {
	t.Helper()
	opp, err := r.CreateOpportunity(ctx, CreateOpportunityInput{
		IdempotencyKey: uniqueSuffix("test-create"), Name: "测试商机", CustomerID: uniqueSuffix("C"),
		CustomerName: "测试客户", ExpectedAmount: "10000.00",
		Items: []CreateOpportunityItemInput{
			{ProductID: uniqueSuffix("P"), ProductSKU: "SKU-1", ProductName: "测试产品", UOMID: "EA", Qty: "2", QuotedUnitPrice: "500.00"},
		},
		DeptID: "12", DeptPath: "/1/12/", OwnerID: "u_test_owner",
	})
	if err != nil {
		t.Fatalf("CreateOpportunity 失败: %v", err)
	}
	return opp
}

func TestCreateOpportunity_真实建档(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "crm_opportunity_rw", "crm_opportunity")

	opp := createTestOpportunity(t, ctx, r)
	if opp.Status != StatusOpen {
		t.Fatalf("期望 OPEN，实际 %q", opp.Status)
	}
	if opp.StageName != "初步接洽" {
		t.Fatalf("期望起始阶段是 sort_order 最小的'初步接洽'，实际 %q", opp.StageName)
	}
	if opp.Probability != 10 {
		t.Fatalf("期望起始阶段默认概率 10，实际 %d", opp.Probability)
	}
	if opp.WeightedAmount != "1000.00" {
		t.Fatalf("期望加权金额 10000.00×10%%=1000.00，实际 %q", opp.WeightedAmount)
	}
	if len(opp.Items) != 1 || opp.Items[0].Subtotal != "1000.00" {
		t.Fatalf("期望商机行 subtotal=2×500.00=1000.00，实际 %+v", opp.Items)
	}
	if opp.DeptID != "12" || opp.DeptPath != "/1/12/" || opp.OwnerID != "u_test_owner" {
		t.Fatalf("创建时快照的归属字段不对：%+v", opp)
	}
}

// TestCreateOpportunity_重复idempotencyKey幂等返回同一条 验证 claim-first
// 幂等：同一个 idempotency_key 重复提交不会建出第二条商机。
func TestCreateOpportunity_重复idempotencyKey幂等返回同一条(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "crm_opportunity_rw", "crm_opportunity")

	key := uniqueSuffix("test-idem-create")
	in := CreateOpportunityInput{
		IdempotencyKey: key, Name: "幂等测试商机", CustomerID: uniqueSuffix("C"), CustomerName: "客户",
		ExpectedAmount: "1000.00",
		Items:          []CreateOpportunityItemInput{{ProductID: "P1", Qty: "1", QuotedUnitPrice: "10.00"}},
	}
	first, err := r.CreateOpportunity(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.CreateOpportunity(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("同一个 idempotency_key 应该返回同一条商机，实际 first=%s second=%s", first.ID, second.ID)
	}
}

// TestMarkWon_真实转WON且Outbox记录且事件真的发布到总线 是 Task 13 明文
// 要求的核心验证：真订阅 crm.opportunity.won.v1，真跑
// besdk.StartOutboxPump，断言事件真的从 event_outbox 被推到 NATS 总线，
// 不是只看 event_outbox 表里躺着一行就算数。
func TestMarkWon_真实转WON且Outbox记录且事件真的发布到总线(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "crm_opportunity_rw", "crm_opportunity")
	opp := createTestOpportunity(t, ctx, r)

	nc, err := nats.Connect(natsURLForTest(t))
	if err != nil {
		t.Fatalf("连接 NATS 失败: %v", err)
	}
	defer nc.Close()

	msgCh := make(chan *nats.Msg, 32) // 留够空间容纳其它测试遗留的旧 PENDING 行
	sub, err := nc.Subscribe("crm.opportunity.won.v1", func(m *nats.Msg) { msgCh <- m })
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	nc.Flush()

	won, err := r.MarkWon(ctx, MarkWonInput{IdempotencyKey: uniqueSuffix("test-win"), ID: opp.ID})
	if err != nil {
		t.Fatalf("MarkWon 失败: %v", err)
	}
	if won.Status != StatusWon {
		t.Fatalf("期望 WON，实际 %q", won.Status)
	}
	if won.WonAt.IsZero() {
		t.Fatal("期望 won_at 已经落库")
	}
	if won.Probability != 100 {
		t.Fatalf("期望赢单后 probability=100，实际 %d", won.Probability)
	}

	// 断言 Outbox 记录确实存在，状态是 PENDING（还没被推送前）。
	// TEST_PG_DSN 用管理凭据连接（同 erp-sales 既有测试的判据），跨
	// schema 直接查不需要 SET LOCAL ROLE。
	var outboxStatus, payloadRaw string
	if err := db.QueryRowContext(ctx, `
		SELECT status, payload::text FROM crm_opportunity.event_outbox
		WHERE subject = 'crm.opportunity.won.v1' AND aggregate_id = $1`, opp.ID,
	).Scan(&outboxStatus, &payloadRaw); err != nil {
		t.Fatalf("查 event_outbox 失败: %v", err)
	}
	if outboxStatus != "PENDING" {
		t.Fatalf("推送前期望 status=PENDING，实际 %q", outboxStatus)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadRaw), &payload); err != nil {
		t.Fatal(err)
	}
	// ⭐ owner_id/dept_path 两个字段最容易漏（设计计划 §4.1）——直接断言
	// 它们确实在 Outbox payload 里，且值等于建档时的快照。
	if payload["owner_id"] != "u_test_owner" {
		t.Fatalf("owner_id 缺失或不对：%v", payload["owner_id"])
	}
	if payload["dept_path"] != "/1/12/" {
		t.Fatalf("dept_path 缺失或不对：%v", payload["dept_path"])
	}
	if payload["customer_id"] != opp.CustomerID {
		t.Fatalf("customer_id 不对：%v", payload["customer_id"])
	}

	// 真跑 Outbox 推送循环，断言事件真的到达 NATS——不是只看表里有一行。
	pumpCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	go func() { _ = besdk.StartOutboxPump(pumpCtx, db, "crm_opportunity", nc, slog.Default()) }()

	// ⚠️ 这个 subject 是全部商机共用的，Postgres 里可能还躺着其它测试
	// （如 TestMarkWon_已终态不能重复赢单，它调用一次成功的 MarkWon 但
	// 从不跑推送循环去清空）留下的旧 PENDING 行——真实生产环境里同一个
	// subject 本来就有多个聚合根的事件，正确的消费者行为是按内容过滤，
	// 不是假设"收到的第一条就是我要的那条"。循环读直到匹配或超时。
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-msgCh:
			var received map[string]any
			if err := json.Unmarshal(msg.Data, &received); err != nil {
				t.Fatal(err)
			}
			if received["opportunity_id"] == opp.ID {
				return
			}
		case <-deadline:
			t.Fatal("超时：crm.opportunity.won.v1 没有真的发布到 NATS（或一直没等到本商机对应的那条）")
		}
	}
}

// TestMarkWon_已终态不能重复赢单 验证状态机：WON/LOST/CANCELLED 都不能
// 再次 MarkWon。
func TestMarkWon_已终态不能重复赢单(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "crm_opportunity_rw", "crm_opportunity")
	opp := createTestOpportunity(t, ctx, r)

	if _, err := r.MarkWon(ctx, MarkWonInput{IdempotencyKey: uniqueSuffix("win-1"), ID: opp.ID}); err != nil {
		t.Fatal(err)
	}
	_, err := r.MarkWon(ctx, MarkWonInput{IdempotencyKey: uniqueSuffix("win-2"), ID: opp.ID})
	if err == nil {
		t.Fatal("已经 WON 的商机不该能再次 MarkWon")
	}
	if !errors.Is(err, ErrOpportunityTerminal) {
		t.Fatalf("期望 ErrOpportunityTerminal，实际 %v", err)
	}
}

// TestMarkLost_真实转LOST且发布事件 验证输单链路：转 LOST、记录原因、
// 发布 crm.opportunity.lost.v1。
func TestMarkLost_真实转LOST且发布事件(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "crm_opportunity_rw", "crm_opportunity")
	opp := createTestOpportunity(t, ctx, r)

	lost, err := r.MarkLost(ctx, MarkLostInput{IdempotencyKey: uniqueSuffix("test-lose"), ID: opp.ID, Reason: "价格谈不拢"})
	if err != nil {
		t.Fatalf("MarkLost 失败: %v", err)
	}
	if lost.Status != StatusLost {
		t.Fatalf("期望 LOST，实际 %q", lost.Status)
	}
	if lost.LostReason != "价格谈不拢" {
		t.Fatalf("期望记录失单原因，实际 %q", lost.LostReason)
	}

	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM crm_opportunity.event_outbox
		WHERE subject = 'crm.opportunity.lost.v1' AND aggregate_id = $1`, opp.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("期望恰好一条 crm.opportunity.lost.v1 Outbox 记录，实际 %d", count)
	}
}

// TestMarkLost_必须填原因 验证契约要求：失单必须记原因（设计计划 §8，
// 借鉴 Odoo loss reason）。
func TestMarkLost_必须填原因(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "crm_opportunity_rw", "crm_opportunity")
	opp := createTestOpportunity(t, ctx, r)

	_, err := r.MarkLost(ctx, MarkLostInput{IdempotencyKey: uniqueSuffix("test-lose-noreason"), ID: opp.ID, Reason: ""})
	if err == nil {
		t.Fatal("reason 为空应该报错")
	}
}

// TestChangeStage_真实写历史且发布事件 验证阶段推进：更新 stage_id/
// probability、写 opportunity_stage_history、发布
// crm.opportunity.stage_changed.v1。
func TestChangeStage_真实写历史且发布事件(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "crm_opportunity_rw", "crm_opportunity")
	opp := createTestOpportunity(t, ctx, r)

	stages, err := r.ListStages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var proposalStageID string
	for _, s := range stages {
		if s.Code == "PROPOSAL" {
			proposalStageID = s.ID
		}
	}
	if proposalStageID == "" {
		t.Fatal("未找到 PROPOSAL 阶段——迁移播种是否跑过？")
	}

	changed, err := r.ChangeStage(ctx, ChangeStageInput{
		IdempotencyKey: uniqueSuffix("test-stage"), ID: opp.ID, ToStageID: proposalStageID, ChangedBy: "u_test_owner",
	})
	if err != nil {
		t.Fatalf("ChangeStage 失败: %v", err)
	}
	if changed.StageID != proposalStageID {
		t.Fatalf("期望 stage_id=%s，实际 %s", proposalStageID, changed.StageID)
	}
	if changed.Probability != 50 {
		t.Fatalf("期望用 PROPOSAL 的默认概率 50，实际 %d", changed.Probability)
	}

	var historyCount int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM crm_opportunity.opportunity_stage_history WHERE opportunity_id = $1`,
		opp.ID).Scan(&historyCount); err != nil {
		t.Fatal(err)
	}
	if historyCount != 1 {
		t.Fatalf("期望恰好一条阶段流转历史，实际 %d", historyCount)
	}

	var eventCount int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM crm_opportunity.event_outbox
		WHERE subject = 'crm.opportunity.stage_changed.v1' AND aggregate_id = $1`, opp.ID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("期望恰好一条 crm.opportunity.stage_changed.v1 Outbox 记录，实际 %d", eventCount)
	}
}

// TestChangeStage_probability手工覆盖 验证设计计划 §9 第 4 条：阶段带
// 默认值 + 允许手工覆盖。
func TestChangeStage_probability手工覆盖(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "crm_opportunity_rw", "crm_opportunity")
	opp := createTestOpportunity(t, ctx, r)

	stages, err := r.ListStages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	toStageID := stages[1].ID // NEEDS_CONFIRMED，默认概率 30
	override := 0             // 手工覆盖成 0——0 也是合法值，必须能和"没传"区分
	changed, err := r.ChangeStage(ctx, ChangeStageInput{
		IdempotencyKey: uniqueSuffix("test-stage-override"), ID: opp.ID, ToStageID: toStageID, ProbabilityOverride: &override,
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Probability != 0 {
		t.Fatalf("期望手工覆盖生效为 0，实际 %d", changed.Probability)
	}
}

// TestUpdateOpportunity_版本冲突 验证乐观锁：传入的 version 与库里不一致
// 时拒绝更新。
func TestUpdateOpportunity_版本冲突(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "crm_opportunity_rw", "crm_opportunity")
	opp := createTestOpportunity(t, ctx, r)

	_, err := r.UpdateOpportunity(ctx, UpdateOpportunityInput{
		IdempotencyKey: uniqueSuffix("test-update-conflict"), ID: opp.ID, Version: opp.Version + 999,
		Name: "改名", ExpectedAmount: "20000.00",
	})
	if err == nil {
		t.Fatal("version 不一致应该报冲突错误")
	}
}

// TestUpdateOpportunity_真实更新 验证正常路径：version 对得上，改名/
// 金额/预计成交日/负责人全部生效。
func TestUpdateOpportunity_真实更新(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "crm_opportunity_rw", "crm_opportunity")
	opp := createTestOpportunity(t, ctx, r)

	updated, err := r.UpdateOpportunity(ctx, UpdateOpportunityInput{
		IdempotencyKey: uniqueSuffix("test-update"), ID: opp.ID, Version: opp.Version,
		Name: "改名后的商机", ExpectedAmount: "20000.00", OwnerID: "u_new_owner",
	})
	if err != nil {
		t.Fatalf("UpdateOpportunity 失败: %v", err)
	}
	if updated.Name != "改名后的商机" || updated.ExpectedAmount != "20000.00" || updated.OwnerID != "u_new_owner" {
		t.Fatalf("更新没有生效：%+v", updated)
	}
	if updated.Version != opp.Version+1 {
		t.Fatalf("期望 version 递增，期望 %d 实际 %d", opp.Version+1, updated.Version)
	}
}

