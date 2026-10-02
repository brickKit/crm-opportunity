// Package migrations 把这个目录下的 .sql 文件嵌进二进制：迁移入口
// （backend/cmd/migrate）用 be-sdk-go 的 migrate.Main 执行它们，镜像里不用另拷
// .sql 文件。
//
// 嵌入必须挨着 .sql 文件所在的目录做：Go 的 embed 不允许路径里出现 ".."，
// 不能在别的包里 //go:embed ../../migrations。
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
