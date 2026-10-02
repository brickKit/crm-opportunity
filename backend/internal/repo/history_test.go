package repo

import (
	"context"
	"testing"

	"github.com/brickKit/crm-opportunity/v2/backend/testdb"
)

// TestListStageHistory_按时间先后返回每次流转：从哪到哪、这次设定的概率、
// 谁改的。建档本身不是一次流转，不算一行。
func TestListStageHistory_按时间先后返回每次流转(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, testdb.Role, testdb.Schema)
	opp := createTestOpportunity(t, ctx, r)

	stages, err := r.ListStages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byCode := map[string]string{}
	for _, s := range stages {
		byCode[s.Code] = s.ID
	}
	override := 45
	if _, err := r.ChangeStage(ctx, ChangeStageInput{IdempotencyKey: uniqueSuffix("hist-1"), ID: opp.ID,
		ToStageID: byCode["PROPOSAL"], ChangedBy: "u_first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ChangeStage(ctx, ChangeStageInput{IdempotencyKey: uniqueSuffix("hist-2"), ID: opp.ID,
		ToStageID: byCode["NEEDS_CONFIRMED"], ProbabilityOverride: &override, ChangedBy: "u_second"}); err != nil {
		t.Fatal(err)
	}

	got, err := r.ListStageHistory(ctx, opp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("期望两次流转，实际 %d：%+v", len(got), got)
	}
	first, second := got[0], got[1]
	if first.FromStageID != byCode["INITIAL_CONTACT"] || first.ToStageID != byCode["PROPOSAL"] ||
		first.Probability == nil || *first.Probability != 50 || first.ChangedBy != "u_first" {
		t.Fatalf("第一次流转不对：%+v", first)
	}
	if second.FromStageID != byCode["PROPOSAL"] || second.ToStageID != byCode["NEEDS_CONFIRMED"] ||
		second.Probability == nil || *second.Probability != 45 || second.ChangedBy != "u_second" {
		t.Fatalf("第二次流转不对（概率应是手工覆盖的 45）：%+v", second)
	}
	if second.ChangedAt.Before(first.ChangedAt) {
		t.Fatalf("应按时间先后排列：%v 在 %v 之前", second.ChangedAt, first.ChangedAt)
	}
}
