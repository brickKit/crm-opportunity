# quotes：请求报价与销售单据回来

商机与 `erp/sales` 之间只经事件的那一半（设计 OP05、OP08，决定 A71）。

## 负责与不负责

- 负责：表 `quote_request`、`sales_link`；请求报价（在商机的变更里写请求、发 `crm.opportunity.quote_requested.v1`，事实身份 = 请求身份）；订阅的处理 `OnSales`（在 SDK 的事务里，不调用任何组件）：带本组件商机参考的报价与订单按版本记下，报价答复它的请求，拒绝的事实把请求标为拒绝，按设置自动赢单（`AutoWins`；经 `opportunities.ChangeIn` 与 `Win`，没有用户操作者）。
- 不负责：商机本身的规则（`opportunities`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `request.go` | `RequestQuote`（OP05） |
| `sales.go` | `AutoWins`、`OnSales`（OP08）、`rejected` |
| `sales_test.go` | 单元测试：三种设置下哪些单据与状态赢单（集成测试只跑 `order_confirmed`） |

## 依赖与被依赖

- 依赖：`core`、`opportunities`。
- 被依赖：入口（路由与订阅）。

## 表

`quote_request`、`sales_link`。

## 规则在哪

设计 OP05、OP08；`erp/sales` 的 QR01–QR05（它怎样答请求）。

## 测试

`sales_test.go`；`itest/quotes_integration_test.go`（`quotes`）。
