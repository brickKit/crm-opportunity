package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/jackc/pgx/v5/stdlib"
	"go.opentelemetry.io/otel/trace/noop"

	besdk "github.com/brickKit/be-sdk-go"
	"github.com/brickKit/crm-opportunity/v2/backend/internal/client"
	"github.com/brickKit/crm-opportunity/v2/backend/internal/repo"
	"github.com/brickKit/crm-opportunity/v2/backend/internal/service"
	"github.com/brickKit/crm-opportunity/v2/backend/testdb"
)

// REST 层测试：用 besdk.NewGinEngine 造真实的 engine（错误到状态码的映射、
// panic 恢复都在），handler 挂在一个把 Claims 塞进 ctx 的测试中间件后面——
// 验签与权限键判定是 SDK 的事，由 SDK 自己的测试守；这里测的是参数、
// 数据范围、响应形状和状态码。

func testRuntime() *besdk.Runtime {
	return &besdk.Runtime{
		Registry: besdk.NewRegistry(),
		Logger:   slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Tracer:   noop.NewTracerProvider().Tracer("test"),
	}
}

func testRepo(t *testing.T) *repo.Repo {
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
	return repo.New(db, testdb.Role, testdb.Schema)
}

func testService(r *repo.Repo) *service.Service {
	return service.New(r, client.New(besdk.NewConfig(nil), client.DefaultTimeout), slog.Default())
}

var seq int64

func uniqueSuffix(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), atomic.AddInt64(&seq, 1))
}

// engineAs 返回一个以 claims 身份访问的 engine，路由与生产相同、只是跳过验签。
func engineAs(svc *service.Service, claims besdk.Claims) *gin.Engine {
	gin.SetMode(gin.TestMode)
	eng := besdk.NewGinEngine(testRuntime())
	asCaller := func(c *gin.Context) {
		c.Request = c.Request.WithContext(besdk.ContextWithClaims(c.Request.Context(), claims))
		c.Next()
	}
	g := eng.Group("/crm/opportunity", asCaller)
	g.GET("/opportunities", listOpportunitiesHandler(svc))
	g.GET("/opportunities/funnel", funnelHandler(svc))
	g.GET("/opportunities/:id", getOpportunityHandler(svc))
	g.PATCH("/opportunities/:id", updateOpportunityHandler(svc))
	g.GET("/opportunities/:id/stage-history", stageHistoryHandler(svc))
	return eng
}

func do(t *testing.T, eng *gin.Engine, method, path, body string) (int, map[string]any) {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, req)
	var out map[string]any
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("响应不是合法 JSON：%v（%s）", err, w.Body.String())
		}
	}
	return w.Code, out
}

func createOpp(t *testing.T, r *repo.Repo, deptPath, ownerID, amount string) *repo.Opportunity {
	t.Helper()
	opp, err := r.CreateOpportunity(context.Background(), repo.CreateOpportunityInput{
		IdempotencyKey: uniqueSuffix("http-create"), Name: "REST 测试商机", CustomerID: uniqueSuffix("C"),
		CustomerName: "REST 测试客户", ExpectedAmount: amount,
		Items:  []repo.CreateOpportunityItemInput{{ProductID: uniqueSuffix("P"), Qty: "1", QuotedUnitPrice: amount}},
		DeptID: "1", DeptPath: deptPath, OwnerID: ownerID,
	})
	if err != nil {
		t.Fatalf("建测试商机失败: %v", err)
	}
	return opp
}

// GET /opportunities/{id} 的响应带 order_id：没有订单时是空串（字段在，前端
// 不用区分"缺字段"和"空"）。
func TestGetOpportunity_REST响应带order_id(t *testing.T) {
	r := testRepo(t)
	owner := "u_" + uniqueSuffix("rest-owner")
	opp := createOpp(t, r, "/1/12/", owner, "100.00")
	eng := engineAs(testService(r), besdk.Claims{Sub: owner, DeptPath: "/1/12/"})

	code, body := do(t, eng, http.MethodGet, "/crm/opportunity/opportunities/"+opp.ID, "")
	if code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d：%v", code, body)
	}
	v, ok := body["order_id"]
	if !ok || v != "" {
		t.Fatalf("期望 order_id 字段存在且为空串，实际 %v（存在=%v）", v, ok)
	}
}

// GET /opportunities/{id}/stage-history：响应 {history: [{from_stage_id,
// to_stage_id, probability, changed_by, changed_at}]}；别人范围里的商机答 404。
func TestStageHistory_REST形状与范围外404(t *testing.T) {
	r := testRepo(t)
	svc := testService(r)
	owner := "u_" + uniqueSuffix("hist-owner")
	opp := createOpp(t, r, "/1/12/", owner, "100.00")
	stages, err := r.ListStages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ChangeStage(context.Background(), repo.ChangeStageInput{
		IdempotencyKey: uniqueSuffix("rest-hist"), ID: opp.ID, ToStageID: stages[2].ID, ChangedBy: owner}); err != nil {
		t.Fatal(err)
	}

	path := "/crm/opportunity/opportunities/" + opp.ID + "/stage-history"
	code, body := do(t, engineAs(svc, besdk.Claims{Sub: owner, DeptPath: "/1/12/"}), http.MethodGet, path, "")
	if code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d：%v", code, body)
	}
	history, _ := body["history"].([]any)
	if len(history) != 1 {
		t.Fatalf("期望一条历史，实际 %v", body)
	}
	row, _ := history[0].(map[string]any)
	for _, k := range []string{"from_stage_id", "to_stage_id", "probability", "changed_by", "changed_at"} {
		if _, ok := row[k]; !ok {
			t.Fatalf("历史行缺字段 %s：%v", k, row)
		}
	}
	if row["to_stage_id"] != stages[2].ID || row["changed_by"] != owner || row["probability"] != float64(stages[2].DefaultProbability) {
		t.Fatalf("历史行内容不对：%v", row)
	}

	other := engineAs(svc, besdk.Claims{Sub: "u_" + uniqueSuffix("other"), DeptPath: "/1/34/"})
	if code, body := do(t, other, http.MethodGet, path, ""); code != http.StatusNotFound {
		t.Fatalf("别的部门、不是负责人：期望 404，实际 %d：%v", code, body)
	}
}

// PATCH /opportunities/{id} 带过期的 version：409（契约声明的版本冲突），库里不变。
func TestUpdateOpportunity_REST版本过期答409(t *testing.T) {
	r := testRepo(t)
	owner := "u_" + uniqueSuffix("conflict-owner")
	opp := createOpp(t, r, "/1/12/", owner, "100.00")
	eng := engineAs(testService(r), besdk.Claims{Sub: owner, DeptPath: "/1/12/"})

	body := fmt.Sprintf(`{"idempotency_key":%q,"version":%d,"name":"改名","expected_amount":"200.00","owner_id":%q}`,
		uniqueSuffix("conflict"), opp.Version+1, owner)
	code, out := do(t, eng, http.MethodPatch, "/crm/opportunity/opportunities/"+opp.ID, body)
	if code != http.StatusConflict {
		t.Fatalf("期望 409，实际 %d：%v", code, out)
	}
	got, err := r.GetOpportunity(context.Background(), opp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != opp.Version || got.ExpectedAmount != "100.00" {
		t.Fatalf("版本冲突不该改库：%+v", got)
	}
}
