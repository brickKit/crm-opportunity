package repo

import (
	"context"
	"testing"
)

// TestListOpportunities_org维前缀过滤 与 TestListOpportunities_owner维过滤
// 补 ListOpportunities 的真实数据权限边界覆盖（同 erp-sales
// ListOrders 的既有判据，见其 scope_test.go）。
//
// ⚠️ 这条覆盖此前是缺的：repo 包原来只有 TestInScope 一个纯函数单元
// 测试（scope_test.go），验证的是 Opportunity.InScope 这个判定谓词本身
// 对不对；但 ListOpportunities 的真实过滤不走这个谓词，是直接拼 SQL
// WHERE（owner_id = $n 或 dept_path LIKE $n || '%'，由 ViewMine 二选一），
// 两条代码路径完全独立——谓词测对了不代表 SQL 真的照它过滤。这两条
// 补的是"创建两条归属不同的真实商机，只授权一边，断言另一边看不到"，
// 不是"用同一条数据反复断言"。

// createOppWithScope 建一条真实 OPEN 商机，dept_path/owner_id 可指定
// （createTestOpportunity 固定死这两个字段，这里需要能控制）。
func createOppWithScope(t *testing.T, ctx context.Context, r *Repo, deptPath, ownerID string) *Opportunity {
	t.Helper()
	opp, err := r.CreateOpportunity(ctx, CreateOpportunityInput{
		IdempotencyKey: uniqueSuffix("test-list-scope"), Name: "测试商机", CustomerID: uniqueSuffix("C"),
		CustomerName: "测试客户", ExpectedAmount: "10000.00",
		Items: []CreateOpportunityItemInput{
			{ProductID: uniqueSuffix("P"), ProductSKU: "SKU-1", ProductName: "测试产品", UOMID: "EA", Qty: "2", QuotedUnitPrice: "500.00"},
		},
		DeptID: "1", DeptPath: deptPath, OwnerID: ownerID,
	})
	if err != nil {
		t.Fatalf("CreateOpportunity 失败: %v", err)
	}
	return opp
}

func TestListOpportunities_org维前缀过滤(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "crm_opportunity_rw", "crm_opportunity")

	east := createOppWithScope(t, ctx, r, "/1/12/", "u_"+uniqueSuffix("east-rep"))
	south := createOppWithScope(t, ctx, r, "/1/34/", "u_"+uniqueSuffix("south-rep"))

	// 只授权 /1/12/ 这条前缀——应该只看到东区的商机。
	res, err := r.ListOpportunities(ctx, ListInput{PageSize: 200, ScopePrefix: "/1/12/"})
	if err != nil {
		t.Fatal(err)
	}
	foundEast, foundSouth := false, false
	for _, o := range res.Opportunities {
		if o.ID == east.ID {
			foundEast = true
		}
		if o.ID == south.ID {
			foundSouth = true
		}
	}
	if !foundEast {
		t.Fatal("授权东区前缀后应该能看到东区商机")
	}
	if foundSouth {
		t.Fatal("授权东区前缀不该看到南区商机")
	}
}

func TestListOpportunities_owner维过滤(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, "crm_opportunity_rw", "crm_opportunity")
	ownerA := "u_" + uniqueSuffix("owner-a")
	ownerB := "u_" + uniqueSuffix("owner-b")

	// 两条商机部门路径相同（都在 /1/），只有 owner 不同——排除"其实是
	// org 维碰巧也过滤对了"这个假阳性。
	mine := createOppWithScope(t, ctx, r, "/1/", ownerA)
	notMine := createOppWithScope(t, ctx, r, "/1/", ownerB)

	// ViewMine=true 时只用 ScopeOwner，不管 ScopePrefix。
	res, err := r.ListOpportunities(ctx, ListInput{PageSize: 200, ViewMine: true, ScopeOwner: ownerA, ScopePrefix: "/1/"})
	if err != nil {
		t.Fatal(err)
	}
	foundMine, foundNotMine := false, false
	for _, o := range res.Opportunities {
		if o.ID == mine.ID {
			foundMine = true
		}
		if o.ID == notMine.ID {
			foundNotMine = true
		}
	}
	if !foundMine {
		t.Fatal("ViewMine 应该能看到自己的商机")
	}
	if foundNotMine {
		t.Fatal("ViewMine 不该看到别人的商机，即使部门路径相同")
	}
}
