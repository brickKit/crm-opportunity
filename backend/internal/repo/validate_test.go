package repo

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/brickKit/crm-opportunity/v2/backend/testdb"
)

// 金额、数量、概率的入参必须在写库之前严格校验：PostgreSQL 的 NUMERIC 认
// 'NaN'（原样落库，比任何数都大，CHECK 拦不住，漏斗合计整列变 NaN）、认科学
// 计数法 '1e3'、超过两位的小数会被悄悄四舍五入；概率越界、数量为 0 由表上的
// CHECK 拦下，但那是数据库错误，调用方拿到的是 500，不是 400。这些全部应该是
// ErrInvalidArgument，而且一行都不写进库。

var badAmounts = []string{"NaN", "nan", "Inf", "Infinity", "-Infinity", "1e3", "1E3", "-1", "+1", "1/3",
	"abc", "1,5", " 1", "1.", ".5", "100.005", "12345678901234567", ""}

func TestCreateOpportunity_金额不是普通十进制时拒绝且不落库(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, testdb.Role, testdb.Schema)

	base := func() CreateOpportunityInput {
		return CreateOpportunityInput{
			IdempotencyKey: uniqueSuffix("bad-amount"), Name: "金额校验", CustomerID: uniqueSuffix("C"),
			ExpectedAmount: "100.00",
			Items:          []CreateOpportunityItemInput{{ProductID: uniqueSuffix("P"), Qty: "1", QuotedUnitPrice: "100.00"}},
			DeptPath:       "/1/12/", OwnerID: "u_validate",
		}
	}
	expectRejected := func(name string, in CreateOpportunityInput) {
		t.Helper()
		opp, err := r.CreateOpportunity(ctx, in)
		if !errors.Is(err, ErrInvalidArgument) {
			got := "<nil>"
			if opp != nil {
				got = "expected_amount=" + opp.ExpectedAmount
			}
			t.Errorf("%s：期望 ErrInvalidArgument，实际 err=%v，写进库的商机：%s", name, err, got)
		}
	}

	for _, bad := range badAmounts {
		in := base()
		in.ExpectedAmount = bad
		expectRejected("expected_amount="+bad, in)
	}
	for _, bad := range []string{"NaN", "Inf", "1e3", "0", "-1", "abc", "1.1234567", "1234567890123"} {
		in := base()
		in.Items[0].Qty = bad
		expectRejected("qty="+bad, in)
	}
	for _, bad := range []string{"NaN", "Inf", "1e3", "-1", "100.005", ""} {
		in := base()
		in.Items[0].QuotedUnitPrice = bad
		expectRejected("quoted_unit_price="+bad, in)
	}
	for _, bad := range []int{-1, 101, 1000} {
		in := base()
		p := bad
		in.ProbabilityOverride = &p
		expectRejected("probability="+strconv.Itoa(bad), in)
	}

	// 合法的边界值照常写入
	in := base()
	in.ExpectedAmount = "9999999999999999.99"
	in.Items[0].Qty = "0.000001"
	in.Items[0].QuotedUnitPrice = "0"
	zero := 0
	in.ProbabilityOverride = &zero
	if _, err := r.CreateOpportunity(ctx, in); err != nil {
		t.Fatalf("合法边界值应写入成功：%v", err)
	}
}

func TestUpdateOpportunity_金额不是普通十进制时拒绝且不改库(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, testdb.Role, testdb.Schema)
	opp := createTestOpportunity(t, ctx, r)

	for _, bad := range badAmounts {
		_, err := r.UpdateOpportunity(ctx, UpdateOpportunityInput{
			IdempotencyKey: uniqueSuffix("bad-update"), ID: opp.ID, Version: opp.Version,
			Name: opp.Name, ExpectedAmount: bad, OwnerID: opp.OwnerID,
		})
		// 版本冲突也包着 ErrInvalidArgument：要的是金额校验拒绝，不是前一次误写入之后的版本冲突
		if !errors.Is(err, ErrInvalidArgument) || errors.Is(err, ErrVersionConflict) {
			t.Errorf("expected_amount=%q：期望金额校验报 ErrInvalidArgument，实际 %v", bad, err)
		}
	}
	got, err := r.GetOpportunity(ctx, opp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExpectedAmount != opp.ExpectedAmount || got.Version != opp.Version {
		t.Fatalf("非法金额不该改库：原 %s v%d，现 %s v%d", opp.ExpectedAmount, opp.Version, got.ExpectedAmount, got.Version)
	}
}

func TestChangeStage_概率越界时拒绝(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, testdb.Role, testdb.Schema)
	opp := createTestOpportunity(t, ctx, r)
	stages, err := r.ListStages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []int{-1, 101} {
		p := bad
		_, err := r.ChangeStage(ctx, ChangeStageInput{
			IdempotencyKey: uniqueSuffix("bad-prob"), ID: opp.ID, ToStageID: stages[1].ID, ProbabilityOverride: &p,
		})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("probability_override=%d：期望 ErrInvalidArgument，实际 %v", bad, err)
		}
	}
}
