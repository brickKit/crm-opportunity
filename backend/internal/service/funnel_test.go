package service

import (
	"context"
	"log/slog"
	"testing"

	besdk "github.com/brickKit/be-sdk-go"

	"github.com/brickKit/crm-opportunity/v2/backend/internal/client"
	"github.com/brickKit/crm-opportunity/v2/backend/internal/repo"
)

// 漏斗按调用者的数据范围统计，视角与列表完全一致：view=mine 只数本人负责的，
// 否则数本部门及下级的——切换，不是 OR。两个用户、两个部门，各自看到的数不同，
// 也看不到别人部门的商机。只统计 OPEN；加权金额按十进制算到分再求和。

type funnelFixture struct {
	r      *repo.Repo
	svc    *Service
	stages []*repo.Stage
}

func (f *funnelFixture) create(t *testing.T, deptPath, owner, amount string, stageIdx int) *repo.Opportunity {
	t.Helper()
	ctx := context.Background()
	opp := createOpportunityWithScope(t, ctx, f.r, "1", deptPath, owner)
	// 金额：createOpportunityWithScope 固定 1000.00，这里按需改掉
	opp, err := f.r.UpdateOpportunity(ctx, repo.UpdateOpportunityInput{
		IdempotencyKey: uniqueSuffix("funnel-amount"), ID: opp.ID, Version: opp.Version,
		Name: opp.Name, ExpectedAmount: amount, OwnerID: owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stageIdx > 0 {
		if opp, err = f.r.ChangeStage(ctx, repo.ChangeStageInput{
			IdempotencyKey: uniqueSuffix("funnel-stage"), ID: opp.ID, ToStageID: f.stages[stageIdx].ID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return opp
}

func byStage(t *testing.T, got []repo.FunnelStage) map[string]repo.FunnelStage {
	t.Helper()
	m := map[string]repo.FunnelStage{}
	for _, s := range got {
		m[s.StageID] = s
	}
	return m
}

func TestFunnel_mine与dept两个用户看到不同的数(t *testing.T) {
	db := testDB(t)
	r := repo.New(db, "crm_opportunity_rw", "crm_opportunity")
	f := &funnelFixture{r: r, svc: New(r, client.New(e2eConfig(), client.DefaultTimeout), slog.Default())}
	stages, err := r.ListStages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.stages = stages

	east := "/funnel-" + uniqueSuffix("east") + "/"
	south := "/funnel-" + uniqueSuffix("south") + "/"
	alice, bob, carol := "u_"+uniqueSuffix("alice"), "u_"+uniqueSuffix("bob"), "u_"+uniqueSuffix("carol")

	// alice（东区）：初步接洽两条（10%），方案报价一条（50%）；另有一条赢单、一条输单不计入
	f.create(t, east, alice, "100.05", 0)        // 10% → 10.01（10.005 进一）
	f.create(t, east, alice, "200.00", 0)        // 10% → 20.00
	f.create(t, east+"sub/", alice, "100.05", 2) // 50% → 50.03；下级部门，dept 视角也算
	won := f.create(t, east, alice, "999.00", 0)
	if _, err := r.MarkWon(context.Background(), repo.MarkWonInput{IdempotencyKey: uniqueSuffix("funnel-won"), ID: won.ID}); err != nil {
		t.Fatal(err)
	}
	lost := f.create(t, east, alice, "888.00", 0)
	if _, err := r.MarkLost(context.Background(), repo.MarkLostInput{IdempotencyKey: uniqueSuffix("funnel-lost"), ID: lost.ID, Reason: "测试"}); err != nil {
		t.Fatal(err)
	}
	// bob（东区，同部门）：初步接洽一条
	f.create(t, east, bob, "300.00", 0) // 10% → 30.00
	// carol（南区）：初步接洽一条
	f.create(t, south, carol, "400.00", 0)

	aliceCtx := besdk.ContextWithClaims(context.Background(), besdk.Claims{Sub: alice, DeptPath: east})
	bobCtx := besdk.ContextWithClaims(context.Background(), besdk.Claims{Sub: bob, DeptPath: east})
	carolCtx := besdk.ContextWithClaims(context.Background(), besdk.Claims{Sub: carol, DeptPath: south})
	first, proposal := stages[0].ID, stages[2].ID

	check := func(name string, got []repo.FunnelStage, stageID string, count int, expected, weighted string) {
		t.Helper()
		if len(got) != len(stages) {
			t.Fatalf("%s：每个阶段都要列出（%d 个），实际 %d", name, len(stages), len(got))
		}
		s := byStage(t, got)[stageID]
		if s.Count != count || s.ExpectedAmount != expected || s.WeightedAmount != weighted {
			t.Fatalf("%s 阶段 %s：期望 count=%d expected=%s weighted=%s，实际 %+v", name, stageID, count, expected, weighted, s)
		}
	}

	aliceMine, err := f.svc.Funnel(aliceCtx, true)
	if err != nil {
		t.Fatal(err)
	}
	check("alice mine", aliceMine, first, 2, "300.05", "30.01")
	check("alice mine", aliceMine, proposal, 1, "100.05", "50.03")

	aliceDept, err := f.svc.Funnel(aliceCtx, false)
	if err != nil {
		t.Fatal(err)
	}
	check("alice dept", aliceDept, first, 3, "600.05", "60.01")
	check("alice dept", aliceDept, proposal, 1, "100.05", "50.03")

	bobMine, err := f.svc.Funnel(bobCtx, true)
	if err != nil {
		t.Fatal(err)
	}
	check("bob mine", bobMine, first, 1, "300.00", "30.00")
	check("bob mine", bobMine, proposal, 0, "0.00", "0.00")

	carolDept, err := f.svc.Funnel(carolCtx, false)
	if err != nil {
		t.Fatal(err)
	}
	check("carol dept（看不到东区）", carolDept, first, 1, "400.00", "40.00")
	check("carol dept（看不到东区）", carolDept, proposal, 0, "0.00", "0.00")
}
