// Package testdb 只给连库测试用：本组件在测试库里的数据库身份（schema 与
// SET LOCAL ROLE 的角色）。
//
// 默认是 make test-db-init 在 brickkit_test_db 里建好的那一套（本项目注册表里的
// schema 与角色）。TEST_PG_SCHEMA / TEST_PG_ROLE 换成别的身份，整套测试照跑：
// 生产代码不认任何固定的名字，测试也不认，名字只在这一处。
//
// ⚠️ 生产代码不许 import 它（它读进程环境）；make module-check 查这一条。
package testdb

import (
	"os"
	"strings"
)

var (
	// Role 是测试里 repo.New / besdk.WithTx 切换到的角色，相当于部署时的 PG_USER。
	Role = envOr("TEST_PG_ROLE", "crm_opportunity_rw")
	// Schema 是测试里的 search_path，相当于部署时的 PG_SCHEMA。
	Schema = envOr("TEST_PG_SCHEMA", "crm_opportunity")
)

// Q 把 SQL 里的 {schema} 换成测试 schema，给绕过 repo、直接查表的断言用。
func Q(sql string) string { return strings.ReplaceAll(sql, "{schema}", Schema) }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
