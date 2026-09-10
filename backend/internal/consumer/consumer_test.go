// 真实 Postgres + NATS 集成测试（同 erp-sales consumer_test.go 的既有
// 判据）：真订阅、真发布、真等待，不是直接调 repo 层函数假装事件已经
// 到过。
package consumer

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nats-io/nats.go"
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

// publishEvent 的 aggregateID 必须是每次调用都不同的值——besdk.Consume
// 的 event_inbox 按 (subject, aggregate_id, version) 做单调去重，且这张
// 表是持久化的（不会在两次 go test 之间清空），同 erp-sales/erp-inventory/
// erp-finance 已经踩过的坑（consumer_test.go 的既有约定）。
func publishEvent(t *testing.T, nc *nats.Conn, subject, aggregateID string, version int64, payload string) {
	t.Helper()
	msg := &nats.Msg{Subject: subject, Data: []byte(payload), Header: nats.Header{}}
	msg.Header.Set("X-Aggregate-Id", aggregateID)
	msg.Header.Set("X-Version", strconv.FormatInt(version, 10))
	msg.Header.Set("X-Hop-Count", "0")
	if err := nc.PublishMsg(msg); err != nil {
		t.Fatal(err)
	}
}

func uniqueID(t *testing.T) string {
	t.Helper()
	return "cust-consumer-test-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

// TestConsumer_客户事件维护展示快照 验证消费 mdm.customer.created.v1——
// 真订阅、真发布、真等待，断言 customer_snapshots 真的被写入/更新。
func TestConsumer_客户事件维护展示快照(t *testing.T) {
	db := testDB(t)
	nc, err := nats.Connect(natsURLForTest(t))
	if err != nil {
		t.Fatalf("连接 NATS 失败: %v", err)
	}
	defer nc.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- Start(ctx, db, "crm_opportunity_rw", "crm_opportunity", nc, nil) }()
	// NATS core pub/sub 没有队列：订阅是异步注册的，发布早于订阅注册完成
	// 那条消息就直接丢了，不会重投（同 erp-sales consumer_test.go 的
	// 既有判据，Start 内部对每个 subject 各起一个 goroutine 调
	// besdk.Consume，给它们一点真实注册时间）。
	time.Sleep(150 * time.Millisecond)

	customerID := uniqueID(t)
	publishEvent(t, nc, "mdm.customer.created.v1", customerID, 1,
		`{"id":"`+customerID+`","name":"真实消费测试客户"}`)

	deadline := time.Now().Add(5 * time.Second)
	var name string
	for time.Now().Before(deadline) {
		err := db.QueryRowContext(context.Background(),
			`SELECT name FROM crm_opportunity.customer_snapshots WHERE customer_id = $1`, customerID).Scan(&name)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if name != "真实消费测试客户" {
		t.Fatalf("期望 customer_snapshots.name='真实消费测试客户'，实际 %q（可能事件没有真的被消费到）", name)
	}

	// version 单调更新：旧版本事件不该覆盖新值（§3.10）。
	publishEvent(t, nc, "mdm.customer.updated.v1", customerID, 0, // version=0 < 1，应被忽略
		`{"id":"`+customerID+`","name":"不该生效的旧版本名字"}`)
	time.Sleep(500 * time.Millisecond)
	if err := db.QueryRowContext(context.Background(),
		`SELECT name FROM crm_opportunity.customer_snapshots WHERE customer_id = $1`, customerID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "真实消费测试客户" {
		t.Fatalf("旧版本事件不该覆盖，期望仍是'真实消费测试客户'，实际 %q", name)
	}
}

