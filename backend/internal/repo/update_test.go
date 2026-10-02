package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brickKit/crm-opportunity/v2/backend/testdb"
)

// UpdateOpportunity 的入参里 owner_id 与 expected_close_date 在契约里是可选的：
// 照契约只带要改的字段的调用方，不能因为没带就把负责人清空、把预计成交日抹掉。
// 负责人被清空的后果很重：view=mine 里看不到了，赢单事件带 owner_id ""，
// 没有部门的人建的商机（dept_path 为空）从此谁都看不到。

func createOpportunityWithCloseDate(t *testing.T, ctx context.Context, r *Repo, closeDate time.Time) *Opportunity {
	t.Helper()
	opp, err := r.CreateOpportunity(ctx, CreateOpportunityInput{
		IdempotencyKey: uniqueSuffix("test-create-close"), Name: "带预计成交日的商机", CustomerID: uniqueSuffix("C"),
		CustomerName: "测试客户", ExpectedAmount: "10000.00", ExpectedCloseDate: closeDate,
		Items: []CreateOpportunityItemInput{
			{ProductID: uniqueSuffix("P"), ProductSKU: "SKU-1", ProductName: "测试产品", UOMID: "EA", Qty: "2", QuotedUnitPrice: "500.00"},
		},
		DeptID: "12", DeptPath: "/1/12/", OwnerID: "u_keep_owner",
	})
	if err != nil {
		t.Fatalf("CreateOpportunity 失败: %v", err)
	}
	return opp
}

func TestUpdateOpportunity_没带负责人与预计成交日时保留原值(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, testdb.Role, testdb.Schema)
	closeDate := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	opp := createOpportunityWithCloseDate(t, ctx, r, closeDate)

	updated, err := r.UpdateOpportunity(ctx, UpdateOpportunityInput{
		IdempotencyKey: uniqueSuffix("test-update-keep"), ID: opp.ID, Version: opp.Version,
		Name: "只改名和金额", ExpectedAmount: "20000.00",
	})
	if err != nil {
		t.Fatalf("UpdateOpportunity 失败: %v", err)
	}
	if updated.Name != "只改名和金额" || updated.ExpectedAmount != "20000.00" {
		t.Fatalf("带了的字段没有生效：%+v", updated)
	}
	if updated.OwnerID != "u_keep_owner" {
		t.Fatalf("没带 owner_id 不该清空负责人：期望 u_keep_owner，实际 %q", updated.OwnerID)
	}
	if got := updated.ExpectedCloseDate.Format("2006-01-02"); got != "2026-12-31" {
		t.Fatalf("没带 expected_close_date 不该抹掉预计成交日：期望 2026-12-31，实际 %q", got)
	}
}

func TestUpdateOpportunity_带了新负责人与新预计成交日时照改(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, testdb.Role, testdb.Schema)
	opp := createOpportunityWithCloseDate(t, ctx, r, time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC))

	updated, err := r.UpdateOpportunity(ctx, UpdateOpportunityInput{
		IdempotencyKey: uniqueSuffix("test-update-change"), ID: opp.ID, Version: opp.Version,
		Name: opp.Name, ExpectedAmount: opp.ExpectedAmount, OwnerID: "u_next_owner",
		ExpectedCloseDate: time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("UpdateOpportunity 失败: %v", err)
	}
	if updated.OwnerID != "u_next_owner" || updated.ExpectedCloseDate.Format("2006-01-02") != "2027-03-01" {
		t.Fatalf("带了的负责人 / 预计成交日没有生效：owner=%q close=%s", updated.OwnerID, updated.ExpectedCloseDate.Format("2006-01-02"))
	}
}

// name 没有“不改”的合法空值：空名字（gRPC 不带 name 时就是空串）拒绝，不能把名字写成空。
func TestUpdateOpportunity_名称为空时拒绝且不改库(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, testdb.Role, testdb.Schema)
	opp := createTestOpportunity(t, ctx, r)

	_, err := r.UpdateOpportunity(ctx, UpdateOpportunityInput{
		IdempotencyKey: uniqueSuffix("test-update-noname"), ID: opp.ID, Version: opp.Version,
		ExpectedAmount: "20000.00", OwnerID: opp.OwnerID,
	})
	if !errors.Is(err, ErrInvalidArgument) || errors.Is(err, ErrVersionConflict) {
		t.Fatalf("空名称：期望 ErrInvalidArgument（不是版本冲突），实际 %v", err)
	}
	got, err := r.GetOpportunity(ctx, opp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != opp.Name || got.Version != opp.Version {
		t.Fatalf("空名称不该改库：原 %q v%d，现 %q v%d", opp.Name, opp.Version, got.Name, got.Version)
	}
}
