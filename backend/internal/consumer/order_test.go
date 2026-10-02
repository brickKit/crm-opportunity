package consumer

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/brickKit/crm-opportunity/v2/backend/internal/repo"
)

// 商机 order_id 的回填：erp/sales 在 sales.order.created.v1 里带
// source_opportunity_id，本组件按它找到自己的商机、写上 order_id。
// 这里真订阅、真发布、真等待，订阅的是测试私有 subject（本机正在跑的组件
// 容器只订阅生产 subject，收不到这些消息）。

// syncBuffer 让测试读日志时不与消费 goroutine 的写入竞争。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type orderFixture struct {
	db      *sql.DB
	nc      *nats.Conn
	r       *repo.Repo
	subject string
	logs    *syncBuffer
}

func startOrderConsumer(t *testing.T) *orderFixture {
	t.Helper()
	db := testDB(t)
	nc, err := nats.Connect(natsURLForTest(t))
	if err != nil {
		t.Fatalf("连接 NATS 失败: %v", err)
	}
	t.Cleanup(nc.Close)

	stamp := time.Now().UnixNano()
	s := subjects{
		CustomerCreated: fmt.Sprintf("test.mdm.customer.created.v1.%d", stamp),
		CustomerUpdated: fmt.Sprintf("test.mdm.customer.updated.v1.%d", stamp),
		OrderCreated:    fmt.Sprintf("test.sales.order.created.v1.%d", stamp),
	}
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = start(ctx, db, "crm_opportunity_rw", "crm_opportunity", nc, logger, s) }()
	// NATS core 订阅是异步注册的，早于注册完成的消息直接丢掉、不重投。
	time.Sleep(150 * time.Millisecond)

	return &orderFixture{db: db, nc: nc, r: repo.New(db, "crm_opportunity_rw", "crm_opportunity"), subject: s.OrderCreated, logs: logs}
}

func (f *orderFixture) wonOpportunity(t *testing.T) *repo.Opportunity {
	t.Helper()
	ctx := context.Background()
	opp, err := f.r.CreateOpportunity(ctx, repo.CreateOpportunityInput{
		IdempotencyKey: uniqueID(t) + "-create", Name: "回填测试商机", CustomerID: uniqueID(t),
		CustomerName: "回填测试客户", ExpectedAmount: "1000.00",
		Items:  []repo.CreateOpportunityItemInput{{ProductID: uniqueID(t), Qty: "1", QuotedUnitPrice: "1000.00"}},
		DeptID: "12", DeptPath: "/1/12/", OwnerID: "u_backfill_owner",
	})
	if err != nil {
		t.Fatalf("建商机失败: %v", err)
	}
	won, err := f.r.MarkWon(ctx, repo.MarkWonInput{IdempotencyKey: uniqueID(t) + "-win", ID: opp.ID})
	if err != nil {
		t.Fatalf("赢单失败: %v", err)
	}
	return won
}

// publishOrder 发一条订单创建事件；aggregate 是订单 id（与 erp/sales 的信封一致）。
func (f *orderFixture) publishOrder(t *testing.T, orderID string, version int64, payload string) {
	t.Helper()
	publishEvent(t, f.nc, f.subject, orderID, version, payload)
}

func orderPayload(orderID, sourceOpportunityID string) string {
	return fmt.Sprintf(`{"order_id":%q,"order_no":"SO-TEST","customer_id":"1","items":[],"total_amount":"1.00","version":1,"source_opportunity_id":%q}`,
		orderID, sourceOpportunityID)
}

// waitInbox 等到某个订单事件真的被处理并提交（handler 返回 nil 时 inbox 行随
// 事务一起提交；返回 error 时随事务回滚）。
func (f *orderFixture) waitInbox(t *testing.T, orderID string, version int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := f.db.QueryRow(`SELECT count(*) FROM crm_opportunity.event_inbox
			WHERE subject = $1 AND aggregate_id = $2 AND version = $3`, f.subject, orderID, version).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("超时：订单 %s v%d 的事件没有被处理并提交（handler 报错时 inbox 行会回滚）", orderID, version)
}

func (f *orderFixture) get(t *testing.T, id string) *repo.Opportunity {
	t.Helper()
	o, err := f.r.GetOpportunity(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestOrderCreated_按source_opportunity_id回填order_id(t *testing.T) {
	f := startOrderConsumer(t)
	opp := f.wonOpportunity(t)
	orderID := uniqueID(t)

	f.publishOrder(t, orderID, 1, orderPayload(orderID, opp.ID))
	f.waitInbox(t, orderID, 1)

	got := f.get(t, opp.ID)
	if got.OrderID != orderID {
		t.Fatalf("期望 order_id=%s，实际 %q", orderID, got.OrderID)
	}
	if got.Version != opp.Version+1 {
		t.Fatalf("回填是一次变更，version 应从 %d 变成 %d，实际 %d", opp.Version, opp.Version+1, got.Version)
	}
}

// 同一张订单的事件再来一次（重复投递）或以更新的版本再来一次（erp/sales 的
// 订单更新也可能重发同一 subject），order_id 已经是它：什么都不改。
func TestOrderCreated_重复投递不重复变更(t *testing.T) {
	f := startOrderConsumer(t)
	opp := f.wonOpportunity(t)
	orderID := uniqueID(t)

	f.publishOrder(t, orderID, 1, orderPayload(orderID, opp.ID))
	f.waitInbox(t, orderID, 1)
	after := f.get(t, opp.ID)

	f.publishOrder(t, orderID, 1, orderPayload(orderID, opp.ID)) // 原样重投：inbox 去重
	f.publishOrder(t, orderID, 2, orderPayload(orderID, opp.ID)) // 新版本、同一订单：值相同，跳过
	f.waitInbox(t, orderID, 2)

	got := f.get(t, opp.ID)
	if got.OrderID != orderID || got.Version != after.Version {
		t.Fatalf("重复投递不该再改商机：期望 order_id=%s version=%d，实际 order_id=%q version=%d",
			orderID, after.Version, got.OrderID, got.Version)
	}
}

// 乱序：同一张订单的旧版本事件晚于新版本到达，按版本单调丢弃，不报错也不改值。
func TestOrderCreated_旧版本晚到被丢弃(t *testing.T) {
	f := startOrderConsumer(t)
	opp := f.wonOpportunity(t)
	orderID := uniqueID(t)

	f.publishOrder(t, orderID, 2, orderPayload(orderID, opp.ID))
	f.waitInbox(t, orderID, 2)
	after := f.get(t, opp.ID)

	f.publishOrder(t, orderID, 1, orderPayload(orderID, opp.ID))
	time.Sleep(500 * time.Millisecond)

	got := f.get(t, opp.ID)
	if got.OrderID != orderID || got.Version != after.Version {
		t.Fatalf("旧版本晚到不该改商机：期望 order_id=%s version=%d，实际 order_id=%q version=%d",
			orderID, after.Version, got.OrderID, got.Version)
	}
}

// 第二张订单声称来自同一个商机：一个商机只转一次单，第二张是数据问题——记
// ERROR、不覆盖第一张。
func TestOrderCreated_不同订单不覆盖并记ERROR(t *testing.T) {
	f := startOrderConsumer(t)
	opp := f.wonOpportunity(t)
	first, second := uniqueID(t)+"-a", uniqueID(t)+"-b"

	f.publishOrder(t, first, 1, orderPayload(first, opp.ID))
	f.waitInbox(t, first, 1)
	f.publishOrder(t, second, 1, orderPayload(second, opp.ID))
	f.waitInbox(t, second, 1)

	got := f.get(t, opp.ID)
	if got.OrderID != first {
		t.Fatalf("第二张订单不该覆盖第一张：期望 order_id=%s，实际 %q", first, got.OrderID)
	}
	logs := f.logs.String()
	if !strings.Contains(logs, "level=ERROR") || !strings.Contains(logs, second) {
		t.Fatalf("期望记一条带第二张订单 id 的 ERROR 日志，实际日志：\n%s", logs)
	}
}

// 不是从商机转来的订单（字段缺失或为空），以及本组件没有的商机 id：忽略、
// 正常提交（inbox 行在），不报错。
func TestOrderCreated_不是本组件的商机就忽略(t *testing.T) {
	f := startOrderConsumer(t)
	opp := f.wonOpportunity(t)

	noField := uniqueID(t) + "-nofield"
	f.publishOrder(t, noField, 1, fmt.Sprintf(`{"order_id":%q,"order_no":"SO-X","total_amount":"1.00"}`, noField))
	f.waitInbox(t, noField, 1)

	empty := uniqueID(t) + "-empty"
	f.publishOrder(t, empty, 1, orderPayload(empty, ""))
	f.waitInbox(t, empty, 1)

	unknown := uniqueID(t) + "-unknown"
	f.publishOrder(t, unknown, 1, orderPayload(unknown, "999999999999"))
	f.waitInbox(t, unknown, 1)

	notNumeric := uniqueID(t) + "-nan"
	f.publishOrder(t, notNumeric, 1, orderPayload(notNumeric, "opp-abc"))
	f.waitInbox(t, notNumeric, 1)

	if got := f.get(t, opp.ID); got.OrderID != "" {
		t.Fatalf("这些订单都不该碰到本测试的商机，实际 order_id=%q", got.OrderID)
	}
}
