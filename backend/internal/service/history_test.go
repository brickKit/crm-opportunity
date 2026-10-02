package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	besdk "github.com/brickKit/be-sdk-go"

	"github.com/brickKit/crm-opportunity/v2/backend/internal/client"
	"github.com/brickKit/crm-opportunity/v2/backend/internal/repo"
)

// stage-history 的数据范围：与单条读取同样的可见性（部门前缀或本人是负责人），
// 看不到的商机回 NotFound——与不存在的商机答得一样，不泄露哪些 id 存在。
func TestGetStageHistory_范围外与不存在一样是NotFound(t *testing.T) {
	db := testDB(t)
	r := repo.New(db, "crm_opportunity_rw", "crm_opportunity")
	svc := New(r, client.New(e2eConfig(), client.DefaultTimeout), slog.Default())

	east := besdk.ContextWithClaims(context.Background(), besdk.Claims{Sub: "u_" + uniqueSuffix("east"), DeptPath: "/1/12/"})
	southSub := "u_" + uniqueSuffix("south")
	south := besdk.ContextWithClaims(context.Background(), besdk.Claims{Sub: southSub, DeptPath: "/1/34/"})

	opp := createOpportunityWithScope(t, east, r, "12", "/1/12/", "u_"+uniqueSuffix("east-rep"))
	stages, err := r.ListStages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ChangeStage(context.Background(), repo.ChangeStageInput{
		IdempotencyKey: uniqueSuffix("svc-hist"), ID: opp.ID, ToStageID: stages[2].ID, ChangedBy: "u_x"}); err != nil {
		t.Fatal(err)
	}

	got, err := svc.GetStageHistory(east, opp.ID)
	if err != nil {
		t.Fatalf("本部门的人应该看得到：%v", err)
	}
	if len(got) != 1 {
		t.Fatalf("期望一次流转，实际 %d", len(got))
	}

	if _, err := svc.GetStageHistory(south, opp.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("别的部门、也不是负责人：期望 ErrNotFound，实际 %v", err)
	}
	if _, err := svc.GetStageHistory(south, "999999999999"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("不存在的商机：期望 ErrNotFound，实际 %v", err)
	}

	// 负责人是本人，即使部门不同也看得到（与单条读取的 OR 一致，两个操作数都来自调用者的 token）
	mine := createOpportunityWithScope(t, south, r, "99", "/9/99/", southSub)
	if _, err := svc.GetStageHistory(south, mine.ID); err != nil {
		t.Fatalf("负责人本人应该看得到：%v", err)
	}
}
