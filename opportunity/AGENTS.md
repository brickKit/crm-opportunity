# opportunity/：入口与组装

组件对外的一个包：`main.go` 与外壳只引用它（`opportunity.Definition()`、`opportunity.New`）。这里只**组装**：把 `internal/` 下业务模块的函数登记成 SDK 要的路由与订阅；业务一行也不写。

## 负责与不负责

- 负责：组件的身份与版本、迁移、自己的配置键 `OPPORTUNITY_AUTO_WIN`、把哪个路由与订阅交给哪个模块。
- 不负责：业务规则（去 [internal/AGENTS.md](../internal/AGENTS.md) 找模块）；资源、操作、维度、事件类型的名字（`internal/core/names.go`）；集成测试（`itest/`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `definition.go` | `Version`、端口、迁移、配置键（`Keys`、`Own`）；`Definition()` 把身份、流、资源与操作（`core.Definitions()`）收成组件定义 |
| `module.go` | `Module` 与 `New`；`Routes`（用户面的每条路由 → 模块的处理函数与操作）、`Subscriptions`（`erp/sales` 的报价、订单与拒绝 → `quotes.OnSales`） |
| `checks.go` | `RecoveryChecks`：整套恢复时审计过的商机的存在性与版本；单号允许缺号，没有作废 |
| `contract_test.go` | 单元测试：清单的 `configSchema` 与 `events`、OpenAPI 与路由一一对应、合并安全 |

迁移在 `migrations/`（整个组件一套）。

## 依赖与被依赖

- 依赖：`internal/core`、`internal/forecast`、`internal/opportunities`、`internal/quotes`。
- 被依赖：`main.go`、外壳、`itest/`。
- 模块不能反过来引用这里（会成环）：模块需要的名字放 `internal/core`。

## 加东西

- 新路由：处理函数写在它所属的模块里，在 `module.go` 的 `Routes` 登记，操作的名字在 `internal/core/names.go`，OpenAPI 改 `contract/openapi.yaml`（`TestTheRoutesAreTheOpenAPIDocument` 核对两边）。

## 测试

`go test ./opportunity/`。
