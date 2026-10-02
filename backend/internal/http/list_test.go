package http

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	besdk "github.com/brickKit/be-sdk-go"
)

// GET /opportunities 的 created_after / created_before（契约声明的查询参数）：
// 不传时只看最近 90 天；传了就按给定的窗口查，否则更早的商机经 REST 永远列不出来。
// 格式不对答 400，不悄悄退回默认窗口。
func TestListOpportunities_REST按created_after与created_before查(t *testing.T) {
	r := testRepo(t)
	owner := "u_" + uniqueSuffix("window-owner")
	opp := createOpp(t, r, "/1/12/", owner, "100.00")
	backdate(t, opp.ID, 200*24*time.Hour)
	eng := engineAs(testService(r), besdk.Claims{Sub: owner, DeptPath: "/1/12/"})

	list := func(query url.Values) (int, []any) {
		t.Helper()
		query.Set("view", "mine")
		code, body := do(t, eng, http.MethodGet, "/crm/opportunity/opportunities?"+query.Encode(), "")
		items, _ := body["opportunities"].([]any)
		return code, items
	}
	at := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339) }

	if code, items := list(url.Values{}); code != http.StatusOK || len(items) != 0 {
		t.Fatalf("不传窗口：期望 200 且看不到 200 天前的商机，实际 %d、%d 条", code, len(items))
	}
	if code, items := list(url.Values{"created_after": {at(300 * 24 * time.Hour)}}); code != http.StatusOK || len(items) != 1 {
		t.Fatalf("created_after=300 天前：期望 200 且列出 1 条，实际 %d、%d 条", code, len(items))
	}
	if code, items := list(url.Values{
		"created_after": {at(300 * 24 * time.Hour)}, "created_before": {at(250 * 24 * time.Hour)},
	}); code != http.StatusOK || len(items) != 0 {
		t.Fatalf("窗口 300–250 天前：期望 200 且 0 条，实际 %d、%d 条", code, len(items))
	}
	for _, bad := range []url.Values{{"created_after": {"2026-13-01"}}, {"created_before": {"yesterday"}}} {
		if code, _ := list(bad); code != http.StatusBadRequest {
			t.Fatalf("%v：期望 400，实际 %d", bad, code)
		}
	}
}

// backdate 把商机的 created_at 往前挪，造一条默认窗口之外的商机（接口不允许指定创建时间）。
func backdate(t *testing.T, id string, d time.Duration) {
	t.Helper()
	db, err := sql.Open("pgx", os.Getenv("TEST_PG_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(context.Background(),
		`UPDATE crm_opportunity.opportunities SET created_at = $1 WHERE id = $2`, time.Now().Add(-d), id); err != nil {
		t.Fatal(err)
	}
}
