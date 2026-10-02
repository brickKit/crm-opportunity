package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	besdk "github.com/brickKit/be-sdk-go"
	"github.com/brickKit/crm-opportunity/v2/backend/internal/client"
	"github.com/brickKit/crm-opportunity/v2/backend/internal/service"
)

// /opportunities/funnel 与 /opportunities/:id 在同一层注册：Gin 若把 funnel
// 当成 :id 的取值，漏斗请求会打到详情 handler，答 400（id 不是数字）。这里用
// 生产的 RegisterRoutes 造真实的 engine，在它前面挂一个全局中间件记下 Gin
// 匹配到的路由模式（c.FullPath()）。没有配 authz 时权限中间件一律 403，不影响
// 判断走的是哪条路由。
func TestRegisterRoutes_funnel不被冒号id吞掉(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eng := besdk.NewGinEngine(testRuntime())
	var matched string
	eng.Use(func(c *gin.Context) {
		matched = c.FullPath()
		c.Next()
	})
	RegisterRoutes(eng, service.New(nil, client.New(besdk.NewConfig(nil), client.DefaultTimeout), nil))

	cases := []struct{ path, want string }{
		{"/crm/opportunity/opportunities/funnel", "/crm/opportunity/opportunities/funnel"},
		{"/crm/opportunity/opportunities/funnel?view=mine", "/crm/opportunity/opportunities/funnel"},
		{"/crm/opportunity/opportunities/123", "/crm/opportunity/opportunities/:id"},
		{"/crm/opportunity/opportunities/123/stage-history", "/crm/opportunity/opportunities/:id/stage-history"},
		{"/crm/opportunity/opportunities", "/crm/opportunity/opportunities"},
	}
	for _, c := range cases {
		matched = ""
		w := httptest.NewRecorder()
		eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, c.path, nil))
		if matched != c.want {
			t.Errorf("GET %s：期望匹配 %s，实际 %q（状态 %d）", c.path, c.want, matched, w.Code)
		}
	}
}
