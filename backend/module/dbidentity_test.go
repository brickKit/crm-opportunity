package module

import (
	"strings"
	"testing"

	besdk "github.com/brickKit/be-sdk-go"
)

// 角色就是 PG_USER，schema 就是 PG_SCHEMA，互不推导（R63）。
func TestDBIdentity_角色取PG_USER不从schema推(t *testing.T) {
	role, schema, err := dbIdentity(besdk.NewConfig(map[string]string{"PG_USER": "any_login", "PG_SCHEMA": "sales_crm"}))
	if err != nil {
		t.Fatal(err)
	}
	if role != "any_login" || schema != "sales_crm" {
		t.Fatalf("期望 role=any_login schema=sales_crm，实际 role=%q schema=%q", role, schema)
	}
}

// 缺键或名字不能拼进 SQL：New 当场失败，不带着坏身份跑到第一个请求。
func TestDBIdentity_缺键或非法名字启动即失败(t *testing.T) {
	cases := map[string]map[string]string{
		"缺 PG_USER":    {"PG_SCHEMA": "any_schema"},
		"缺 PG_SCHEMA":  {"PG_USER": "any_login"},
		"PG_USER 为空":   {"PG_USER": "", "PG_SCHEMA": "any_schema"},
		"PG_USER 带引号":  {"PG_USER": `x"; DROP`, "PG_SCHEMA": "any_schema"},
		"PG_SCHEMA 大写": {"PG_USER": "any_login", "PG_SCHEMA": "CRM"},
	}
	for name, env := range cases {
		if _, _, err := dbIdentity(besdk.NewConfig(env)); err == nil {
			t.Errorf("%s：期望报错，实际通过", name)
		} else if !strings.Contains(err.Error(), "PG_") {
			t.Errorf("%s：错误里要点名是哪个键，实际 %v", name, err)
		}
	}
}
