# core：全组件共用的名字与写法（共用）

每个模块都引用、自己什么业务也不做的东西：名字、资源定义、错误原因与校验、审计与事件的写法、配置。改这里会影响所有模块。

## 负责与不负责

- 负责：组件 ID、依赖的 ID、流与事件类型（自己的与订阅的 `erp/sales` 的）、资源与操作、维度（法人、负责人）、管道的阶段与缺省概率（`OpenStages`）、参数（`names.go`）；资源定义 `Definitions()`；本组件的错误原因与校验、授权（`Check`）、审计（`Change`）、发件表（`Emit`）、单号（`Number`）、幂等的结果、可选的请求体、十进制（`Decimal`）、列表筛选值的形式（`QueryFilters`）（`rules.go`）；生命周期表的通用写法（`lifecycle.go`）；`Base` 与配置键 `OPPORTUNITY_AUTO_WIN`（`base.go`）。
- 不负责：任何业务规则。

## 文件

| 文件 | 内容 |
| --- | --- |
| `base.go` | `Base`：组件的运行时与自动赢单的设置 `AutoWin`；`KeyAutoWin` 与它的三个值 |
| `names.go` | 常量；阶段与 `OpenStages`；`fields`、`Definitions()`（列表能排序、筛选的字段要在这里声明，不声明 SDK 就答 `FIELD_NOT_QUERYABLE`） |
| `rules.go` | `Err…`（`STATE_CONFLICT`、`VERSION_CONFLICT`、`STAGE_ORDER`、`WON_AMOUNT_LOCKED`、`CUSTOMER_NOT_ELIGIBLE`、`REFERENCE_INVALID`）、表单的格式、`Facts`、`Number`、`BindOptional`、`IDResult`、`ResultID`、`Check`、`Invalid`、`NotFound`、`Unsupported`、`Decimal`、`Change`、`Emit`、`QueryFilters` |
| `lifecycle.go` | 生命周期表：`Effect`、`Decision`、`Table`（`Decide`）、`Refused`、造格的 `RefuseCell`、`IgnoreCell`、`ProceedCell`、`MoveCell`；三项核对：`TableProblems`、`DocumentProblems`、`UntestedRules`（写法照 `erp/purchase`，SF30：以后进 SDK） |

## 依赖与被依赖

- 依赖：只有 SDK。
- 被依赖：全部模块与入口。

## 规则在哪

资源、操作、维度：安全 06、安全 07；事件与发件表：装配项目协议 02；审计：协议 03；单号与金额：协议 14。

## 测试

入口的单元测试 `opportunity/contract_test.go`；其余由全部流程测试间接覆盖。
