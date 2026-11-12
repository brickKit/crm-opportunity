# internal/：业务模块

商机的业务，每个模块一个包。只给本仓库引用（Go 的 `internal`）：别的组件编译期就引用不了，跨组件只走契约（决定 0101）。

先按下表找到模块（按层从高到低排），再读那个目录的 `AGENTS.md`。

## 模块

| 包 | 一句话 | 依赖（本组件内） |
| --- | --- | --- |
| `quotes` | 请求报价（OP05）；销售的报价、订单与拒绝回来，自动赢单（OP08） | `core`、`opportunities` |
| `forecast` | 漏斗与预测方法（替换点） | `core`、`masterdata` |
| `opportunities` | 商机与它的生命周期（OP01–OP04、OP06、OP07）；读取与列表；唯一的变更入口 `ChangeIn` | `core`、`masterdata` |
| `masterdata`（共用） | 事务外取证：法人、币种的小数位、客户资格 | `core` |
| `core`（共用） | 名字、资源与操作的定义、错误与校验、生命周期表的通用写法、配置、`Base` | — |

分层（每个包只引用比它低的层，没有环）：

| 层 | 包 |
| --- | --- |
| 4 | `quotes` |
| 3 | `opportunities`、`forecast` |
| 2 | `masterdata` |
| 1 | `core` |

核对：`go list -f '{{.Imports}}' ./internal/<包>`。

## 共同的写法

- **每个模块的函数先收 `m *core.Base`**（组件的运行时与配置），入口包的 `Module` 持有它、登记时适配；模块之间直接调对方导出的函数。
- **名字在 `core`**：资源、操作、维度、事件类型、阶段、错误原因都在 `core/names.go`、`core/rules.go`；资源定义（`core.Definitions()`）是全组件一张表。
- **生命周期是一张表**：商机在哪个状态能做什么，只在 `opportunities/lifecycle.go`（`core.Table`，写法照 `erp/purchase`，SF30：以后进 SDK）；改商机只经 `opportunities.ChangeIn`。
- **事务外取证、事务内写**：主数据的调用在事务之前；写入、审计（`core.Change`）与发件表（`core.Emit`）在同一个事务。

## 加一个模块

一个新的业务能力有自己的表、路由与规则时才建模块（拿不准时放进最近的模块，宁可少切）：建目录、写 `AGENTS.md` 与 `CLAUDE.md`（`@AGENTS.md`），把它加进上面的表与仓库根的模块表，在 `opportunity/module.go` 登记它的路由。

## 测试

模块没有单独的单元测试（生命周期表与 `quotes.AutoWins` 除外）：业务规则由 `itest/` 的流程测试覆盖，每个模块的说明写了它在哪个文件里被测。
