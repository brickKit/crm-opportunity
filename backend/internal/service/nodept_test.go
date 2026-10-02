package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	besdk "github.com/brickKit/be-sdk-go"

	"github.com/brickKit/crm-opportunity/v2/backend/internal/client"
	"github.com/brickKit/crm-opportunity/v2/backend/internal/repo"
	"github.com/brickKit/crm-opportunity/v2/backend/testdb"
)

// 没有部门的人：token 里 dept_path 为空（新账号的默认状态，还没被分到任何部门）。
// 他在 org 维什么都不命中，"本部门及下级"视图是空的，单条读取与写命令只剩
// "我是负责人"这一个理由。空路径不是部门树的根：根部门的人拿到的是 /<根id>/，
// 看全部的显式标记是 "/"。

func noDeptCtx(sub string) context.Context {
	return besdk.ContextWithClaims(context.Background(), besdk.Claims{Sub: sub, DeptPath: ""})
}

func scopeTestService(t *testing.T) (*repo.Repo, *Service) {
	t.Helper()
	r := repo.New(testDB(t), testdb.Role, testdb.Schema)
	return r, New(r, client.New(e2eConfig(), client.DefaultTimeout), slog.Default())
}

// createOppForCustomer 建一条指定客户与归属的商机：同一个客户 id 把一个测试自己
// 建的几条商机圈在一起，列表按客户过滤，不受测试库里别的数据干扰。
func createOppForCustomer(t *testing.T, r *repo.Repo, customerID, deptPath, ownerID string) *repo.Opportunity {
	t.Helper()
	opp, err := r.CreateOpportunity(context.Background(), repo.CreateOpportunityInput{
		IdempotencyKey: uniqueSuffix("nodept-opp"), Name: "无部门测试商机", CustomerID: customerID,
		CustomerName: "无部门测试客户", ExpectedAmount: "1000.00",
		Items: []repo.CreateOpportunityItemInput{
			{ProductID: uniqueSuffix("P"), ProductSKU: "SKU-X", ProductName: "测试产品", UOMID: "EA", Qty: "1", QuotedUnitPrice: "1000.00"},
		},
		DeptID: leafDeptID(deptPath), DeptPath: deptPath, OwnerID: ownerID,
	})
	if err != nil {
		t.Fatalf("建测试商机失败: %v", err)
	}
	return opp
}

func ids(opps []*repo.Opportunity) map[string]bool {
	m := make(map[string]bool, len(opps))
	for _, o := range opps {
		m[o.ID] = true
	}
	return m
}

func TestListOpportunities_无部门的人dept视图看不到任何商机(t *testing.T) {
	r, svc := scopeTestService(t)
	me := "u_" + uniqueSuffix("nodept-me")
	cust := uniqueSuffix("C-nodept-list")
	createOppForCustomer(t, r, cust, "/1/12/", "u_"+uniqueSuffix("east"))
	createOppForCustomer(t, r, cust, "", "u_"+uniqueSuffix("other-nodept"))
	mine := createOppForCustomer(t, r, cust, "", me)

	// 不按客户过滤：整张表里一条都不该看到（不是"恰好这几条看不到"）
	all, err := svc.ListOpportunities(noDeptCtx(me), repo.ListInput{PageSize: 200})
	if err != nil {
		t.Fatalf("dept 视图不该报错：%v", err)
	}
	if len(all.Opportunities) != 0 {
		t.Fatalf("没有部门的人 dept 视图期望空列表，实际 %d 条（第一条 dept_path=%q owner=%q）",
			len(all.Opportunities), all.Opportunities[0].DeptPath, all.Opportunities[0].OwnerID)
	}

	// 自己负责的在 mine 视图里照样看得到
	res, err := svc.ListOpportunities(noDeptCtx(me), repo.ListInput{PageSize: 200, ViewMine: true, CustomerID: cust})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(res.Opportunities); len(got) != 1 || !got[mine.ID] {
		t.Fatalf("mine 视图期望只有自己那一条 %s，实际 %v", mine.ID, got)
	}
}

func TestFunnel_无部门的人dept视图为零(t *testing.T) {
	r, svc := scopeTestService(t)
	me := "u_" + uniqueSuffix("nodept-funnel")
	cust := uniqueSuffix("C-nodept-funnel")
	createOppForCustomer(t, r, cust, "/1/12/", "u_"+uniqueSuffix("east"))
	createOppForCustomer(t, r, cust, "", "u_"+uniqueSuffix("other-nodept"))
	createOppForCustomer(t, r, cust, "", me)

	got, err := svc.Funnel(noDeptCtx(me), false)
	if err != nil {
		t.Fatalf("dept 视图不该报错：%v", err)
	}
	if len(got) == 0 {
		t.Fatal("漏斗应该列出每个阶段（数目为 0），实际一个阶段都没有")
	}
	for _, s := range got {
		if s.Count != 0 || s.ExpectedAmount != "0.00" || s.WeightedAmount != "0.00" {
			t.Fatalf("没有部门的人 dept 视图每个阶段都应为零，阶段 %s 实际 %+v", s.StageID, s)
		}
	}
}

func TestGetOpportunity_无部门的人看别人的商机是NotFound(t *testing.T) {
	r, svc := scopeTestService(t)
	me := "u_" + uniqueSuffix("nodept-get")
	cust := uniqueSuffix("C-nodept-get")
	inDept := createOppForCustomer(t, r, cust, "/1/12/", "u_"+uniqueSuffix("east"))
	noDeptOther := createOppForCustomer(t, r, cust, "", "u_"+uniqueSuffix("other-nodept"))
	mine := createOppForCustomer(t, r, cust, "", me)

	for name, opp := range map[string]*repo.Opportunity{"有部门的别人": inDept, "同样没有部门的别人": noDeptOther} {
		if got, err := svc.GetOpportunity(noDeptCtx(me), opp.ID); !errors.Is(err, repo.ErrNotFound) {
			t.Fatalf("%s的商机：期望 ErrNotFound（与不存在一样），实际 err=%v 商机=%v", name, err, got)
		}
	}
	if _, err := svc.GetOpportunity(noDeptCtx(me), mine.ID); err != nil {
		t.Fatalf("自己负责的商机应该看得到：%v", err)
	}
}

func TestGetStageHistory_无部门的人看别人的是NotFound(t *testing.T) {
	r, svc := scopeTestService(t)
	me := "u_" + uniqueSuffix("nodept-hist")
	cust := uniqueSuffix("C-nodept-hist")
	inDept := createOppForCustomer(t, r, cust, "/1/12/", "u_"+uniqueSuffix("east"))
	noDeptOther := createOppForCustomer(t, r, cust, "", "u_"+uniqueSuffix("other-nodept"))

	for name, opp := range map[string]*repo.Opportunity{"有部门的别人": inDept, "同样没有部门的别人": noDeptOther} {
		if got, err := svc.GetStageHistory(noDeptCtx(me), opp.ID); !errors.Is(err, repo.ErrNotFound) {
			t.Fatalf("%s的商机：期望 ErrNotFound，实际 err=%v 历史=%v", name, err, got)
		}
	}
}

func TestMarkWon_无部门的人赢别人的商机是Forbidden(t *testing.T) {
	r, svc := scopeTestService(t)
	me := "u_" + uniqueSuffix("nodept-win")
	cust := uniqueSuffix("C-nodept-win")
	inDept := createOppForCustomer(t, r, cust, "/1/12/", "u_"+uniqueSuffix("east"))
	noDeptOther := createOppForCustomer(t, r, cust, "", "u_"+uniqueSuffix("other-nodept"))

	for name, opp := range map[string]*repo.Opportunity{"有部门的别人": inDept, "同样没有部门的别人": noDeptOther} {
		if _, err := svc.MarkWon(noDeptCtx(me), opp.ID, uniqueSuffix("nodept-win")); !errors.Is(err, repo.ErrForbidden) {
			t.Fatalf("%s的商机：期望 ErrForbidden，实际 %v", name, err)
		}
		got, err := r.GetOpportunity(context.Background(), opp.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != repo.StatusOpen || got.Version != opp.Version {
			t.Fatalf("%s的商机：被拒的赢单不该改库，实际 status=%s version=%d", name, got.Status, got.Version)
		}
	}
}

// 没有部门的人可以建商机；这条商机的部门快照是空串（不是 SDK 的哨兵值），
// 所以只有他本人看得到：同样没有部门的别人、根标记 "/"、任何真实部门都看不到。
func TestCreateOpportunity_无部门的人建的商机dept_path留空只有本人看得到(t *testing.T) {
	r, svc := scopeTestService(t)
	me := "u_" + uniqueSuffix("nodept-create")
	ctx := noDeptCtx(me)

	deptID, deptPath, ownerID := creatorSnapshot(ctx)
	if deptPath != "" || deptID != "" || ownerID != me {
		t.Fatalf("快照期望 dept_path=\"\" dept_id=\"\" owner_id=%q，实际 dept_path=%q dept_id=%q owner_id=%q",
			me, deptPath, deptID, ownerID)
	}
	if strings.HasPrefix(deptPath, "!") {
		t.Fatalf("快照里写进了哨兵值 %q", deptPath)
	}

	cust := uniqueSuffix("C-nodept-create")
	opp := createOppForCustomer(t, r, cust, deptPath, ownerID)

	if _, err := svc.GetOpportunity(ctx, opp.ID); err != nil {
		t.Fatalf("本人应该看得到自己建的商机：%v", err)
	}
	others := map[string]context.Context{
		"同样没有部门的别人": noDeptCtx("u_" + uniqueSuffix("other-nodept")),
		"根标记 /":     besdk.ContextWithClaims(context.Background(), besdk.Claims{Sub: "u_" + uniqueSuffix("root"), DeptPath: "/"}),
		"根部门 /1/":   besdk.ContextWithClaims(context.Background(), besdk.Claims{Sub: "u_" + uniqueSuffix("head"), DeptPath: "/1/"}),
	}
	for name, octx := range others {
		if _, err := svc.GetOpportunity(octx, opp.ID); !errors.Is(err, repo.ErrNotFound) {
			t.Fatalf("%s：期望 ErrNotFound，实际 %v", name, err)
		}
		res, err := svc.ListOpportunities(octx, repo.ListInput{PageSize: 200, CustomerID: cust})
		if err != nil {
			t.Fatal(err)
		}
		if ids(res.Opportunities)[opp.ID] {
			t.Fatalf("%s：dept 视图不该列出没有部门的人建的商机", name)
		}
	}
}

// 跨组件版本：真的经 mdm-customer / mdm-product 校验后建档，断言写进库的快照。
func TestCreateOpportunity_无部门的人经真实主数据建档dept_path留空(t *testing.T) {
	requireE2EEnv(t)
	r, svc := scopeTestService(t)
	me := "u_" + uniqueSuffix("nodept-e2e")
	ctx := noDeptCtx(me)
	customerID, _ := createRealCustomer(t, ctx)
	productID, _, _ := createRealProduct(t, ctx)

	opp, err := svc.CreateOpportunity(ctx, CreateOpportunityInput{
		IdempotencyKey: uniqueSuffix("nodept-e2e"), Name: "无部门建档", CustomerID: customerID,
		Items:          []CreateOpportunityItem{{ProductID: productID, Qty: "1", QuotedUnitPrice: "10.00"}},
		ExpectedAmount: "10.00",
	})
	if err != nil {
		t.Fatalf("没有部门的人应该能建商机：%v", err)
	}
	got, err := r.GetOpportunity(context.Background(), opp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DeptPath != "" || got.DeptID != "" || got.OwnerID != me {
		t.Fatalf("库里的快照期望 dept_path=\"\" dept_id=\"\" owner_id=%q，实际 %q %q %q", me, got.DeptPath, got.DeptID, got.OwnerID)
	}
}

// 根标记 "/"：作为普通前缀匹配每一条有部门的商机；dept_path 为空的行不是任何
// 部门的，不在其中。
func TestListOpportunities_根标记斜杠看得到所有有部门的商机(t *testing.T) {
	r, svc := scopeTestService(t)
	root := besdk.ContextWithClaims(context.Background(), besdk.Claims{Sub: "u_" + uniqueSuffix("root"), DeptPath: "/"})
	cust := uniqueSuffix("C-root")
	east := createOppForCustomer(t, r, cust, "/1/12/", "u_"+uniqueSuffix("east"))
	other := createOppForCustomer(t, r, cust, "/9/99/34/", "u_"+uniqueSuffix("other-tree"))
	noDept := createOppForCustomer(t, r, cust, "", "u_"+uniqueSuffix("nodept"))

	res, err := svc.ListOpportunities(root, repo.ListInput{PageSize: 200, CustomerID: cust})
	if err != nil {
		t.Fatal(err)
	}
	got := ids(res.Opportunities)
	if len(got) != 2 || !got[east.ID] || !got[other.ID] {
		t.Fatalf("根标记期望看到两条有部门的商机 %s、%s，实际 %v", east.ID, other.ID, got)
	}
	for _, o := range []*repo.Opportunity{east, other} {
		if _, err := svc.GetOpportunity(root, o.ID); err != nil {
			t.Fatalf("根标记应该看得到 %s（dept_path=%s）：%v", o.ID, o.DeptPath, err)
		}
	}
	if _, err := svc.GetOpportunity(root, noDept.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("dept_path 为空的别人的商机：期望 ErrNotFound，实际 %v", err)
	}
}
