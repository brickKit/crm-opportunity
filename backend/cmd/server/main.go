package main

import (
	besdk "github.com/brickKit/be-sdk-go"
	"github.com/brickKit/crm-opportunity/v2/backend/module"
)

// ⚠️ 这个文件只有这一行：装配全在 module.New，单跑与合进外壳才走同一条路。
func main() { besdk.RunStandalone(module.New) }
