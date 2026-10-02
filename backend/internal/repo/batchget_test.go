package repo

import (
	"context"
	"testing"

	"github.com/brickKit/crm-opportunity/v2/backend/testdb"
)

// TestBatchGetOpportunities_缺失的id进missing不报错：契约写明 BatchGet 只返回
// 找到的、缺失的 id 列进 missing_ids——BFF 拿一批 id 来解析，其中一条被删
// 或者根本不存在，不能让整批请求失败。
func TestBatchGetOpportunities_缺失的id进missing不报错(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	r := New(db, testdb.Role, testdb.Schema)
	opp := createTestOpportunity(t, ctx, r)

	found, missing, err := r.BatchGetOpportunities(ctx, []string{opp.ID, "999999999999"})
	if err != nil {
		t.Fatalf("一个 id 不存在不该让整批失败，实际报错：%v", err)
	}
	if len(found) != 1 || found[0].ID != opp.ID {
		t.Fatalf("期望找到 %s，实际 %+v", opp.ID, found)
	}
	if len(missing) != 1 || missing[0] != "999999999999" {
		t.Fatalf("期望 missing=[999999999999]，实际 %v", missing)
	}
}
