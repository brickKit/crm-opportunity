package repo

import (
	"context"
	"errors"
	"testing"
)

// dept 视角下 ScopePrefix 为空只可能是调用方漏填了（SDK 保证调用者的 Prefix
// 永远不是空串：没有部门时是一个什么都不匹配的哨兵值）。repo 不能把它当成
// "不限"——`LIKE '' || '%'` 匹配全部，漏填一次就是所有人看到所有商机。

func TestListOpportunities_dept视图ScopePrefix留空报InvalidArgument(t *testing.T) {
	r := New(testDB(t), "crm_opportunity_rw", "crm_opportunity")
	createOppWithScope(t, context.Background(), r, "/1/12/", "u_"+uniqueSuffix("east"))

	res, err := r.ListOpportunities(context.Background(), ListInput{PageSize: 200})
	if !errors.Is(err, ErrInvalidArgument) {
		n := -1
		if res != nil {
			n = len(res.Opportunities)
		}
		t.Fatalf("dept 视图 ScopePrefix 为空：期望 ErrInvalidArgument，实际 err=%v、%d 条", err, n)
	}
}

func TestFunnel_dept视图ScopePrefix留空报InvalidArgument(t *testing.T) {
	r := New(testDB(t), "crm_opportunity_rw", "crm_opportunity")
	createOppWithScope(t, context.Background(), r, "/1/12/", "u_"+uniqueSuffix("east"))

	got, err := r.Funnel(context.Background(), FunnelInput{})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("dept 视图 ScopePrefix 为空：期望 ErrInvalidArgument，实际 err=%v、%v", err, got)
	}
}

// mine 视角不看 ScopePrefix：留空是正常的，不报错。
func TestListOpportunities_mine视图不需要ScopePrefix(t *testing.T) {
	r := New(testDB(t), "crm_opportunity_rw", "crm_opportunity")
	owner := "u_" + uniqueSuffix("mine-only")
	opp := createOppWithScope(t, context.Background(), r, "", owner)

	res, err := r.ListOpportunities(context.Background(), ListInput{PageSize: 200, ViewMine: true, ScopeOwner: owner})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Opportunities) != 1 || res.Opportunities[0].ID != opp.ID {
		t.Fatalf("mine 视图期望只有 %s，实际 %d 条", opp.ID, len(res.Opportunities))
	}
}
