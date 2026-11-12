# crm/opportunity 的契约

组件 `crm/opportunity` 对外发布的契约，随组件的版本发布（标签 `contract/v3.…`）：

| 文件 | 内容 |
| --- | --- |
| `openapi.yaml` | 用户面：商机的列表、读取、新建、修改、推进、回退、改负责人、请求报价、赢单、输单、重开，漏斗；每个路由的操作（`x-operation`） |
| `events/` | 流 `crm.opportunity`：完整状态 `crm.opportunity.changed.v1`；增量事实 `stage_changed`、`quote_requested`（`erp/sales` 消费，按事实身份生成一张报价草稿）、`won`、`lost` |

系统面没有本组件自己的服务：补发、恢复核对是 SDK 的标准服务。CRM 与 ERP 只经事件（边界卡），所以没有给销售调用的接口；以后有组件要同步调用商机时，在这里加 `proto/`。

依据：装配项目 [be-assembly-standard](https://github.com/brickKit/be-assembly-standard) 的边界卡 `crm.md`、决定 A71、协议 07（引用与资格）、14（单号、金额、列表）；规则 OP01–OP08 在组件的 `docs/design.md`。
