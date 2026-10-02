package http

import (
	"net/http"
	"testing"

	besdk "github.com/brickKit/be-sdk-go"
)

// GET /opportunities/funnel：响应 {stages: [{stage_id, stage_name, count,
// expected_amount, weighted_amount}]}，每个阶段占一行；?view=mine 只数本人负责的，
// 省略或 dept 数本部门及下级——与列表同一个切换。金额是十进制字符串。
func TestFunnel_REST形状与view切换(t *testing.T) {
	r := testRepo(t)
	dept := "/funnel-rest-" + uniqueSuffix("d") + "/"
	me, colleague := "u_"+uniqueSuffix("me"), "u_"+uniqueSuffix("colleague")
	createOpp(t, r, dept, me, "100.05")
	createOpp(t, r, dept, colleague, "200.00")
	eng := engineAs(testService(r), besdk.Claims{Sub: me, DeptPath: dept})

	first := func(view string) map[string]any {
		t.Helper()
		path := "/crm/opportunity/opportunities/funnel"
		if view != "" {
			path += "?view=" + view
		}
		code, body := do(t, eng, http.MethodGet, path, "")
		if code != http.StatusOK {
			t.Fatalf("GET %s：期望 200，实际 %d：%v", path, code, body)
		}
		stages, _ := body["stages"].([]any)
		if len(stages) == 0 {
			t.Fatalf("GET %s：stages 为空：%v", path, body)
		}
		row, _ := stages[0].(map[string]any)
		for _, k := range []string{"stage_id", "stage_name", "count", "expected_amount", "weighted_amount"} {
			if _, ok := row[k]; !ok {
				t.Fatalf("GET %s：阶段行缺字段 %s：%v", path, k, row)
			}
		}
		return row
	}

	if row := first("mine"); row["count"] != float64(1) || row["expected_amount"] != "100.05" {
		t.Fatalf("view=mine：期望 count=1 expected_amount=100.05，实际 %v", row)
	}
	for _, view := range []string{"", "dept"} {
		if row := first(view); row["count"] != float64(2) || row["expected_amount"] != "300.05" {
			t.Fatalf("view=%q：期望 count=2 expected_amount=300.05，实际 %v", view, row)
		}
	}
}
