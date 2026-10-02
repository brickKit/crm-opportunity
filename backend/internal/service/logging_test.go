package service

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/brickKit/crm-opportunity/v2/backend/internal/client"
	"github.com/brickKit/crm-opportunity/v2/backend/internal/repo"
	"github.com/brickKit/crm-opportunity/v2/backend/testdb"
)

// 调用方自己能纠正的错误（参数不合法、找不到、版本过期、终态不能再流转）不是
// 运维要处理的事：SDK 的访问日志与 RED 指标已经记下了状态码，再记 ERROR 只会
// 淹没真正要人处理的那几条（外壳的检查按 ERROR 行找问题）。ERROR 只留给映射成
// Internal / Unknown 的错误。

func TestUpdateOpportunity_调用方错误不记ERROR(t *testing.T) {
	db := testDB(t)
	ctx := e2eTestCtx()
	var logs bytes.Buffer
	r := repo.New(db, testdb.Role, testdb.Schema)
	svc := New(r, client.New(e2eConfig(), client.DefaultTimeout), slog.New(slog.NewTextHandler(&logs, nil)))

	opp := createOpportunityWithScope(t, ctx, r, "12", "/1/12/", "u_e2e_test_owner")

	// 版本过期 → Aborted（409）
	if _, err := svc.UpdateOpportunity(ctx, repo.UpdateOpportunityInput{
		IdempotencyKey: uniqueSuffix("log-stale"), ID: opp.ID, Version: opp.Version + 5, Name: "x", ExpectedAmount: "1.00",
	}); err == nil {
		t.Fatal("version 过期应该报错")
	}
	// 终态不能再流转 → FailedPrecondition（400）
	if _, err := svc.MarkLost(ctx, opp.ID, uniqueSuffix("log-lost"), "测试"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.MarkWon(ctx, opp.ID, uniqueSuffix("log-won")); err == nil {
		t.Fatal("已输单的商机不该能赢单")
	}

	if strings.Contains(logs.String(), "level=ERROR") {
		t.Fatalf("调用方错误不该记 ERROR，实际日志：\n%s", logs.String())
	}
}
