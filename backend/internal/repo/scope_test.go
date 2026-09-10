package repo

import "testing"

// TestInScope 是纯函数单元测试（同 erp-sales Order.InScope 的既有判据）：
// org（dept_path 前缀）或 owner（owner_id 相等）任一命中就算在范围内，
// 用 OR 不是 AND——"本部门及下级"与"我的商机"是同一个人可能同时具备的
// 两种"看得到"的理由。
func TestInScope(t *testing.T) {
	cases := []struct {
		name        string
		deptPath    string
		ownerID     string
		scopePrefix string
		scopeOwner  string
		want        bool
	}{
		{"org 前缀命中", "/1/12/34/", "u_other", "/1/12/", "u_me", true},
		{"owner 精确命中", "/9/99/", "u_me", "/1/12/", "u_me", true},
		{"两者都不命中", "/9/99/", "u_other", "/1/12/", "u_me", false},
		{"scopePrefix 为空（部门树根节点）天然匹配全部", "/9/99/", "u_other", "", "u_me", true},
		{"两者都命中也是 true（OR 不是异或）", "/1/12/", "u_me", "/1/12/", "u_me", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := &Opportunity{DeptPath: c.deptPath, OwnerID: c.ownerID}
			if got := o.InScope(c.scopePrefix, c.scopeOwner); got != c.want {
				t.Fatalf("InScope(%q, %q) with DeptPath=%q OwnerID=%q = %v, want %v",
					c.scopePrefix, c.scopeOwner, c.deptPath, c.ownerID, got, c.want)
			}
		})
	}
}
