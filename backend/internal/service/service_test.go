// 真故障注入测试（同 erp-sales confirm_test.go 的既有判据）：让
// mdm-customer/mdm-product 真的跑起来，真的校验客户/产品存在与状态、
// 真的取展示快照——用 mock 测这条链等于没测，要验的正是跨进程的部分。
//
// ⚠️ 这些测试需要两个真实依赖组件真的在跑，跟 TEST_PG_DSN 一样用环境
// 变量判断要不要跳过：MDM_CUSTOMER_GRPC_ENDPOINT / MDM_PRODUCT_GRPC_ENDPOINT
// （同 besdk.Endpoint() 认的格式，http://host:port）没有全部设置就跳过，
// 不伪造数据也不用内存里的假实现代替。
package service

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	besdk "github.com/brickKit/be-sdk-go"
	"github.com/brickKit/crm-opportunity/backend/internal/client"
	"github.com/brickKit/crm-opportunity/backend/internal/repo"

	customerv1 "github.com/brickKit/crm-opportunity/gen/mdm/customer/v1"
	productv1 "github.com/brickKit/crm-opportunity/gen/mdm/product/v1"
)

func requireE2EEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"MDM_CUSTOMER_GRPC_ENDPOINT", "MDM_PRODUCT_GRPC_ENDPOINT"} {
		if _, ok := os.LookupEnv(k); !ok {
			t.Skipf("未设置 %s，跳过真故障注入测试（需要依赖组件真的在跑）", k)
		}
	}
}

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

var seq int64

func uniqueSuffix(prefix string) string {
	n := atomic.AddInt64(&seq, 1)
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), n)
}

// e2eTestCtx 给"真的调 svc.CreateOpportunity"这条链路的测试造一个带
// Claims 的 ctx——service.CreateOpportunity 会调 besdk.ScopeOf(ctx) 取
// dept_path/owner_id 做创建时快照，这些真故障注入测试直接调 service 层
// （跳过 REST/gRPC 入口本该经过的 RequirePermission），ctx 里从一开始
// 没有 Claims，ScopeOf 会 panic——同 erp-sales tcc/confirm_test.go 的
// e2eTestCtx 是同一个判据。这不影响同一个 ctx 上继续走 client.Customer/
// client.Product 这几个 UserClient 调用（读的是 gRPC metadata，跟
// besdk.ContextWithClaims 用的 context.WithValue 是两条独立机制）。
func e2eTestCtx() context.Context {
	return besdk.ContextWithClaims(context.Background(), besdk.Claims{Sub: "u_e2e_test_owner", DeptPath: "/1/12/"})
}

// createRealCustomer 在真的 mdm-customer 里建一个 ACTIVE 客户，返回其 id。
func createRealCustomer(t *testing.T, ctx context.Context) (id, name string) {
	t.Helper()
	conn, closeConn, err := client.Customer(ctx)
	if err != nil {
		t.Fatalf("拨号 mdm-customer 失败: %v", err)
	}
	defer closeConn()
	custName := "CRM 测试客户 " + uniqueSuffix("cust")
	resp, err := conn.Create(ctx, &customerv1.CreateRequest{
		IdempotencyKey: uniqueSuffix("test-customer"), Name: custName, CreditLimit: "0",
	})
	if err != nil {
		t.Fatalf("mdm-customer.Create 失败: %v", err)
	}
	if resp.Customer.Status != customerv1.CustomerStatus_CUSTOMER_STATUS_ACTIVE {
		t.Fatalf("新建客户期望 ACTIVE，实际 %v", resp.Customer.Status)
	}
	return resp.Customer.Id, resp.Customer.Name
}

// createRealProduct 在真的 mdm-product 里建一个 ACTIVE 产品，返回其 id/sku/name。
func createRealProduct(t *testing.T, ctx context.Context) (id, sku, name string) {
	t.Helper()
	conn, closeConn, err := client.Product(ctx)
	if err != nil {
		t.Fatalf("拨号 mdm-product 失败: %v", err)
	}
	defer closeConn()
	resp, err := conn.Create(ctx, &productv1.CreateRequest{
		IdempotencyKey: uniqueSuffix("test-product"), Name: "CRM 测试产品",
		BaseUomId: "1", TrackingType: productv1.TrackingType_TRACKING_TYPE_NONE, StandardCost: "10.00",
	})
	if err != nil {
		t.Fatalf("mdm-product.Create 失败: %v", err)
	}
	if resp.Product.Status != productv1.ProductStatus_PRODUCT_STATUS_ACTIVE {
		t.Fatalf("新建产品期望 ACTIVE，实际 %v", resp.Product.Status)
	}
	return resp.Product.Id, resp.Product.Sku, resp.Product.Name
}

func TestCreateOpportunity_真实校验客户与产品并取展示快照(t *testing.T) {
	requireE2EEnv(t)
	db := testDB(t)
	ctx := e2eTestCtx()

	r := repo.New(db, "crm_opportunity_rw", "crm_opportunity")
	svc := New(r, slog.Default())

	customerID, customerName := createRealCustomer(t, ctx)
	productID, productSKU, productName := createRealProduct(t, ctx)

	opp, err := svc.CreateOpportunity(ctx, CreateOpportunityInput{
		IdempotencyKey: uniqueSuffix("test-create-opp"), Name: "真实校验测试商机", CustomerID: customerID,
		Items:          []CreateOpportunityItem{{ProductID: productID, Qty: "2", QuotedUnitPrice: "888.00"}},
		ExpectedAmount: "1776.00",
	})
	if err != nil {
		t.Fatalf("CreateOpportunity 失败: %v", err)
	}
	// 断言展示快照确实来自真实的 mdm-customer/mdm-product BatchGet 响应，
	// 不是调用方自己编的字符串。
	if opp.CustomerName != customerName {
		t.Fatalf("customer_name 快照不对：期望 %q，实际 %q", customerName, opp.CustomerName)
	}
	if len(opp.Items) != 1 || opp.Items[0].ProductSKU != productSKU || opp.Items[0].ProductName != productName {
		t.Fatalf("产品快照不对：期望 sku=%q name=%q，实际 %+v", productSKU, productName, opp.Items)
	}
	// dept_id/dept_path/owner_id 从 e2eTestCtx 注入的 Claims 派生。
	if opp.DeptPath != "/1/12/" || opp.DeptID != "12" || opp.OwnerID != "u_e2e_test_owner" {
		t.Fatalf("创建时快照的归属字段不对：%+v", opp)
	}
}

// TestCreateOpportunity_真实客户不存在时拒绝 是 §3 契约要求的直接测试：
// "建档那一刻就要挡住脏数据"——真的问 mdm-customer，不是本地判断。
func TestCreateOpportunity_真实客户不存在时拒绝(t *testing.T) {
	requireE2EEnv(t)
	db := testDB(t)
	ctx := e2eTestCtx()

	r := repo.New(db, "crm_opportunity_rw", "crm_opportunity")
	svc := New(r, slog.Default())

	productID, _, _ := createRealProduct(t, ctx)
	_, err := svc.CreateOpportunity(ctx, CreateOpportunityInput{
		IdempotencyKey: uniqueSuffix("test-create-badcust"), Name: "客户不存在测试", CustomerID: "not-a-real-customer-id",
		Items:          []CreateOpportunityItem{{ProductID: productID, Qty: "1", QuotedUnitPrice: "1.00"}},
		ExpectedAmount: "1.00",
	})
	if err == nil {
		t.Fatal("客户不存在应该被拒绝")
	}
}
