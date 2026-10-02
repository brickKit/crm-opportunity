package module

// 数据库身份来自配置（R63）：schema 是 PG_SCHEMA，角色是 PG_USER，两个都不是
// 代码或迁移里写死的名字。这条测试在测试库里另建一个非默认的 schema 和一个
// 名字跟 schema 毫无关系的登录角色，以那个角色跑迁移，再经 module.New 装出来的
// 模块真的写一条、读一条——单跑（连接池以这个角色登录）与外壳（连接池以外壳
// 自己的身份登录，成员的 PG_USER 是每个事务切换到的角色）两种形态都走一遍。
// 跑完把 schema 和角色都删掉。

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	besdk "github.com/brickKit/be-sdk-go"
	"github.com/brickKit/be-sdk-go/migrate"
	opportunityv1 "github.com/brickKit/crm-opportunity/gen/crm/opportunity/v1"
	customerv1 "github.com/brickKit/mdm-customer/gen/mdm/customer/v1"
	productv1 "github.com/brickKit/mdm-product/gen/mdm/product/v1"

	"github.com/brickKit/crm-opportunity/v2/migrations"
)

func TestModule_非默认schema与任意名字的登录角色(t *testing.T) {
	adminDSN := os.Getenv("TEST_PG_DSN")
	if adminDSN == "" {
		t.Skip("未设置 TEST_PG_DSN")
	}
	natsURL := os.Getenv("TEST_NATS_URL")
	if natsURL == "" {
		t.Skip("未设置 TEST_NATS_URL：模块的后台循环（Outbox 推送、事件消费）要连 NATS")
	}
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	admin, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	pc, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("解析 TEST_PG_DSN: %v", err)
	}
	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("连 NATS: %v", err)
	}
	t.Cleanup(nc.Close)

	// 角色名故意不是 <schema>_rw：角色只能从 PG_USER 来。
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	schema := "r63_opp_" + suffix
	role := "r63_login_" + suffix
	pw := randomHex(t)
	createIdentity(t, admin, pc.Database, schema, role, pw)

	env := map[string]string{
		"PG_HOST": pc.Host, "PG_PORT": strconv.Itoa(int(pc.Port)), "PG_DATABASE": pc.Database,
		"PG_USER": role, "PG_PASSWORD": pw, "PG_SCHEMA": schema,
	}

	// ① 迁移以这个角色运行，它建的东西归它所有。
	if err := migrate.Run(ctx, env, []string{"up"}, migrations.FS); err != nil {
		t.Fatalf("以角色 %s 迁移进 schema %s 失败: %v", role, schema, err)
	}
	assertOwnedBy(t, admin, schema, role)

	deps := startFakeMasterData(t)
	cfg := map[string]string{
		"MDM_CUSTOMER_GRPC_ENDPOINT": "http://" + deps,
		"MDM_PRODUCT_GRPC_ENDPOINT":  "http://" + deps,
	}
	for k, v := range env {
		cfg[k] = v
	}

	// ② 单跑：连接池以这个角色登录。
	t.Run("单跑", func(t *testing.T) {
		dsn, err := besdk.PGDSN(besdk.NewConfig(env))
		if err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		exerciseModule(t, ctx, db, nc, admin, cfg)
	})

	// ③ 外壳：连接池以外壳自己的身份登录（这里是测试库的管理账号），模块每个
	// 事务 SET LOCAL ROLE 到成员配置里的 PG_USER。
	t.Run("外壳", func(t *testing.T) {
		exerciseModule(t, ctx, admin, nc, admin, cfg)
	})
}

// exerciseModule 用 db 装一个模块：先跑后台循环，等它在 cfg 的 schema 里以
// cfg 的角色建好未来的周分区，再经模块自己的 gRPC 面建一条商机、读回来。
func exerciseModule(t *testing.T, ctx context.Context, db *sql.DB, nc *nats.Conn, admin *sql.DB, cfg map[string]string) {
	t.Helper()
	schema, role := cfg["PG_SCHEMA"], cfg["PG_USER"]
	rt := besdk.NewShellRuntime(besdk.ShellModuleConfig{
		ComponentID: "crm/opportunity", ComponentVersion: "test", Env: cfg,
	}, db, nc)
	rt.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	mod, err := New(ctx, rt)
	if err != nil {
		t.Fatalf("module.New: %v", err)
	}

	// 后台循环：分区维护要以 PG_USER 的身份在 PG_SCHEMA 里建分区。上一个子测试
	// 建过的那一张先删掉，这一形态才是真的又建了一次。
	future := mondayOf(time.Now().UTC()).AddDate(0, 0, 28).Format("2006_01_02")
	for _, table := range []string{"event_outbox", "event_inbox"} {
		if _, err := admin.ExecContext(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s.%s_%s`, schema, table, future)); err != nil {
			t.Fatal(err)
		}
	}
	startCtx, stop := context.WithCancel(ctx)
	startErr := make(chan error, 1)
	go func() { startErr <- mod.Start(startCtx) }()
	defer func() {
		stop()
		if err := <-startErr; err != nil {
			t.Errorf("Start 返回错误: %v", err)
		}
	}()
	for _, table := range []string{"event_outbox", "event_inbox"} {
		waitOwned(t, ctx, admin, schema, table+"_"+future, role)
	}

	// 经模块的 gRPC 面写一条、读一条。调用者身份由测试拦截器放进 ctx，相当于
	// 验签之后的样子。
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	owner := "u_r63_" + randomHex(t)[:8]
	gs := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		return h(besdk.ContextWithClaims(ctx, besdk.Claims{Sub: owner, DeptPath: "/1/"}), req)
	}))
	mod.RegisterGRPC(gs)
	go func() { _ = gs.Serve(lis) }()
	defer gs.Stop()
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cli := opportunityv1.NewOpportunityServiceClient(conn)

	created, err := cli.CreateOpportunity(ctx, &opportunityv1.CreateOpportunityRequest{
		IdempotencyKey: "r63-" + randomHex(t), Name: "R63 商机", CustomerId: "C-R63", ExpectedAmount: "100.00",
		Items: []*opportunityv1.CreateOpportunityItemInput{{ProductId: "P-R63", Qty: "1", QuotedUnitPrice: "100.00"}},
	})
	if err != nil {
		t.Fatalf("经模块建商机（schema %s，角色 %s）失败: %v", schema, role, err)
	}
	got, err := cli.GetOpportunity(ctx, &opportunityv1.GetOpportunityRequest{Id: created.Opportunity.Id})
	if err != nil {
		t.Fatalf("经模块读商机失败: %v", err)
	}
	if got.Name != "R63 商机" || got.CustomerName != "R63 客户" || got.OwnerId != owner {
		t.Fatalf("读回来的商机不对：name=%q customer_name=%q owner=%q", got.Name, got.CustomerName, got.OwnerId)
	}
	var n int
	if err := admin.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT count(*) FROM %s.opportunities WHERE id = $1`, schema), got.Id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("商机 %s 应落在 %s.opportunities，实际 %d 行", got.Id, schema, n)
	}
}

func createIdentity(t *testing.T, admin *sql.DB, database, schema, role, pw string) {
	t.Helper()
	// 清理先登记：后面任何一步失败，schema 与角色都不留在测试库里。
	t.Cleanup(func() {
		for _, stmt := range []string{
			fmt.Sprintf(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = '%s'`, role),
			fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema),
			fmt.Sprintf(`DROP OWNED BY %s`, role),
			fmt.Sprintf(`DROP ROLE IF EXISTS %s`, role),
		} {
			if _, err := admin.Exec(stmt); err != nil {
				t.Errorf("清理 %q: %v", stmt, err)
			}
		}
	})
	// BRICKKIT "Before you deploy" 要求的全部：一个 schema，一个在它上面有
	// USAGE + CREATE 的登录角色。
	for _, stmt := range []string{
		fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD '%s'`, role, pw),
		fmt.Sprintf(`CREATE SCHEMA %s`, schema),
		fmt.Sprintf(`GRANT USAGE, CREATE ON SCHEMA %s TO %s`, schema, role),
		fmt.Sprintf(`GRANT CONNECT ON DATABASE %s TO %s`, database, role),
	} {
		if _, err := admin.Exec(stmt); err != nil {
			t.Fatalf("准备测试身份失败（%s）: %v", stmt[:20], err)
		}
	}
}

// assertOwnedBy：schema 里的每张表、分区、序列都归 role。
func assertOwnedBy(t *testing.T, admin *sql.DB, schema, role string) {
	t.Helper()
	rows, err := admin.Query(`
		SELECT c.relname, pg_get_userbyid(c.relowner) FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p', 'S') ORDER BY 1`, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	tables := 0
	for rows.Next() {
		var name, owner string
		if err := rows.Scan(&name, &owner); err != nil {
			t.Fatal(err)
		}
		tables++
		if owner != role {
			t.Errorf("%s.%s 的属主是 %s，应为 %s", schema, name, owner, role)
		}
	}
	if tables == 0 {
		t.Fatalf("schema %s 里一张表都没有", schema)
	}
}

func waitOwned(t *testing.T, ctx context.Context, admin *sql.DB, schema, table, role string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var owner sql.NullString
		err := admin.QueryRowContext(ctx, `
			SELECT pg_get_userbyid(c.relowner) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = $1 AND c.relname = $2`, schema, table).Scan(&owner)
		if err == nil && owner.String == role {
			return
		}
		if err == nil {
			t.Fatalf("分区 %s.%s 的属主是 %s，应为 %s", schema, table, owner.String, role)
		}
		if err != sql.ErrNoRows {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("15 秒内后台循环没有建出分区 %s.%s", schema, table)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func mondayOf(t time.Time) time.Time {
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1-wd)
}

func randomHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// ── 主数据替身：只回答 BatchGet，任何 id 都是一个 ACTIVE 的客户 / 产品 ──

type fakeCustomers struct {
	customerv1.UnimplementedCustomerServiceServer
}

func (fakeCustomers) BatchGet(_ context.Context, req *customerv1.BatchGetRequest) (*customerv1.BatchGetResponse, error) {
	out := &customerv1.BatchGetResponse{}
	for _, id := range req.Ids {
		out.Customers = append(out.Customers, &customerv1.Customer{
			Id: id, Name: "R63 客户", Status: customerv1.CustomerStatus_CUSTOMER_STATUS_ACTIVE,
		})
	}
	return out, nil
}

type fakeProducts struct {
	productv1.UnimplementedProductServiceServer
}

func (fakeProducts) BatchGet(_ context.Context, req *productv1.BatchGetRequest) (*productv1.BatchGetResponse, error) {
	out := &productv1.BatchGetResponse{}
	for _, id := range req.Ids {
		out.Products = append(out.Products, &productv1.Product{
			Id: id, Sku: "SKU-R63", Name: "R63 产品", BaseUomId: "EA", Status: productv1.ProductStatus_PRODUCT_STATUS_ACTIVE,
		})
	}
	return out, nil
}

func startFakeMasterData(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer()
	customerv1.RegisterCustomerServiceServer(gs, fakeCustomers{})
	productv1.RegisterProductServiceServer(gs, fakeProducts{})
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return lis.Addr().String()
}
