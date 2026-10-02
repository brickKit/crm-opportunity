package http

import (
	"fmt"
	"net/http"
	"testing"

	besdk "github.com/brickKit/be-sdk-go"
)

// 没有部门的人经 REST：默认（dept）视图的列表是空的，别人的商机答 404（与不存在
// 一样），自己负责的照样 200。
func TestNoDept_REST列表为空且别人的商机答404(t *testing.T) {
	r := testRepo(t)
	me := "u_" + uniqueSuffix("rest-nodept")
	other := createOpp(t, r, "/1/12/", "u_"+uniqueSuffix("east"), "100.00")
	mine := createOpp(t, r, "", me, "100.00")
	eng := engineAs(testService(r), besdk.Claims{Sub: me, DeptPath: ""})

	code, body := do(t, eng, http.MethodGet, "/crm/opportunity/opportunities", "")
	items, _ := body["opportunities"].([]any)
	if code != http.StatusOK || len(items) != 0 {
		t.Fatalf("默认视图：期望 200 且空列表，实际 %d、%d 条", code, len(items))
	}
	if code, body := do(t, eng, http.MethodGet, "/crm/opportunity/opportunities/"+other.ID, ""); code != http.StatusNotFound {
		t.Fatalf("别人的商机：期望 404，实际 %d：%v", code, body)
	}
	if code, body := do(t, eng, http.MethodGet, "/crm/opportunity/opportunities/"+mine.ID, ""); code != http.StatusOK {
		t.Fatalf("自己负责的商机：期望 200，实际 %d：%v", code, body)
	}
}

// 没有部门的人建的商机 dept_path 为空，只有负责人看得到。照契约 PATCH 只带要改的
// 字段（不带 owner_id）之后，负责人不能被清空：否则这条商机从此谁都看不到。
func TestNoDept_REST改商机不带owner_id时负责人不变且仍看得到(t *testing.T) {
	r := testRepo(t)
	me := "u_" + uniqueSuffix("rest-nodept-patch")
	mine := createOpp(t, r, "", me, "100.00")
	eng := engineAs(testService(r), besdk.Claims{Sub: me, DeptPath: ""})

	body := fmt.Sprintf(`{"idempotency_key":%q,"version":%d,"name":"改了名","expected_amount":"200.00"}`,
		uniqueSuffix("nodept-patch"), mine.Version)
	code, out := do(t, eng, http.MethodPatch, "/crm/opportunity/opportunities/"+mine.ID, body)
	if code != http.StatusOK {
		t.Fatalf("PATCH：期望 200，实际 %d：%v", code, out)
	}
	if out["owner_id"] != me {
		t.Fatalf("没带 owner_id 不该清空负责人：期望 %q，实际 %v", me, out["owner_id"])
	}
	if code, out := do(t, eng, http.MethodGet, "/crm/opportunity/opportunities/"+mine.ID, ""); code != http.StatusOK {
		t.Fatalf("改完之后负责人自己再读：期望 200，实际 %d：%v", code, out)
	}
}
