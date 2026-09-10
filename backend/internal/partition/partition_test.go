package partition

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestMondayOf(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"周一本身", "2026-09-07", "2026-09-07"}, // 2026-09-07 是周一
		{"周四", "2026-09-10", "2026-09-07"},
		{"周日", "2026-09-13", "2026-09-07"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, err := time.Parse("2006-01-02", c.in)
			if err != nil {
				t.Fatal(err)
			}
			want, err := time.Parse("2006-01-02", c.want)
			if err != nil {
				t.Fatal(err)
			}
			if got := mondayOf(in); !got.Equal(want) {
				t.Fatalf("mondayOf(%s) = %s, want %s", c.in, got.Format("2006-01-02"), c.want)
			}
		})
	}
}

// TestEnsureAllWeekly_幂等且与迁移分区命名一致 是真实 Postgres 集成
// 测试：跑两次验证幂等（不能"先建、报 already exists 就崩"），并断言
// 未来分区的命名与迁移脚本手写的分区命名规则一致（同 erp-sales/
// infra-workflow 的既有判据——这是根 docs/dev/实测踩坑记录.md C12 那类
// 坑的直接回归测试：本组件从建仓库起就带这个循环，不能等分区窗口
// 耗尽才发现漏了）。
func TestEnsureAllWeekly_幂等且与迁移分区命名一致(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设置 TEST_PG_DSN")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	if err := ensureAllWeekly(ctx, db, "crm_opportunity_rw", "crm_opportunity"); err != nil {
		t.Fatalf("第一次运行失败: %v", err)
	}
	// 幂等：第二次运行不该报"分区已存在"之类的错误。
	if err := ensureAllWeekly(ctx, db, "crm_opportunity_rw", "crm_opportunity"); err != nil {
		t.Fatalf("第二次运行（应该幂等）失败: %v", err)
	}

	futureWeek := mondayOf(time.Now().UTC()).AddDate(0, 0, 7*lookAheadWeeks)
	expectedName := "crm_opportunity.event_outbox_" + futureWeek.Format("2006_01_02")
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, expectedName).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatalf("期望未来第 %d 周的分区 %s 已经建好", lookAheadWeeks, expectedName)
	}
}
