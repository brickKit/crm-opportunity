# crm/opportunity

商机组件的代码。用法与边界在 `BRICKKIT.md`，为什么这样设计（术语、数据、状态、规则、未决问题）在 [docs/design.md](docs/design.md)。跨组件的规则在装配项目 be-assembly-standard 的边界卡 `crm.md`、决定 A71（商机请求报价，销售生成报价草稿，报价与订单的事件回到商机）、协议 07（客户的资格）、协议 14（单号、金额、列表）、安全 07（维度）。

## 代码地图

代码按业务模块分层。**先读这一节，再进目录**：每个代码目录都有自己的 `AGENTS.md`（同目录的 `CLAUDE.md` 让 Claude Code 读这个目录的文件时自动带上它），写这一层负责什么、不负责什么、每个文件、依赖谁、被谁用、测在哪；读完就知道要不要往下走。

| 目录 | 是什么 |
| --- | --- |
| [`opportunity/`](opportunity/AGENTS.md) | 入口：组件定义与组装（路由、订阅都在这里登记，转给业务模块）；不写业务。`main.go` 与外壳只引用它 |
| [`internal/`](internal/AGENTS.md) | 业务：下面的表里每个模块一个包；模块之间的依赖表在它的 `AGENTS.md`。Go 的 `internal` 让别的组件编译期就引用不了（决定 0101） |
| [`itest/`](itest/AGENTS.md) | 集成测试，按业务流程分文件；它的 `AGENTS.md` 是测试索引（文件 → 流程 → 规则编号） |
| `opportunity/migrations/` | 表，整个组件一套 |
| `contract/` | 本组件发布的契约：`contract/events/`、`contract/openapi.yaml`（没有系统面的 proto） |
| `docs/` | 设计（规则的正文；各层的 `AGENTS.md` 只指过去，不抄） |
| `cmd/seed/` | 种子（`make seed`）：角色（维护、只管自己的销售、CN01 的销售主管）；CN01 220、ES01 40、US01 30 个商机，每个阶段都有，含赢单、输单与重开；部分方案报价阶段的商机发请求报价 |

业务模块（`internal/` 下）：

| 模块 | 负责 | 拥有的表 |
| --- | --- | --- |
| [`opportunities`](internal/opportunities/AGENTS.md) | 商机与它的生命周期（一张表，OP02–OP08 的格）：新建、修改、阶段、改负责人、赢单输单与重开；读取与列表 | `opportunity`、`opportunity_line`、`stage_change` |
| [`quotes`](internal/quotes/AGENTS.md) | 请求报价（OP05）；销售的报价、订单与拒绝回来（OP08），自动赢单 | `quote_request`、`sales_link` |
| [`forecast`](internal/forecast/AGENTS.md) | 漏斗与预测方法（替换点） | — |

模块共用的（不属于某一个业务）：[`masterdata`](internal/masterdata/AGENTS.md)（事务外取证：法人、币种、客户资格）、[`core`](internal/core/AGENTS.md)（名字、资源与操作的定义、错误与校验、生命周期表的通用写法、配置）。

**实现进度**（3.0.0）：契约的路由全部做了；种子在 `cmd/seed/`。加一个模块就在上面两张表里加一行。

| 想改 | 从这里开始 | 然后 |
| --- | --- | --- |
| 商机在某个状态下能不能做某件事；加一个命令 | `docs/design.md` 的“商机”一表（与 `internal/opportunities/lifecycle.go` 逐格相同，单元测试比较） | `internal/opportunities` 的 `ChangeIn`（每次改动都经过它） |
| 管道与阶段、缺省概率 | `internal/core/names.go` 的 `OpenStages` | 设计的术语与 OP03；迁移里 `stage` 的 CHECK |
| 预测方法 | `internal/forecast/funnel.go` 的 `weighted` | 设计的“部署与替换” |
| 一个维度、一个操作、列表能筛选的字段 | `internal/core/names.go` | `docs/design.md` 的命令与查询、`BRICKKIT.md` 的契约索引；路由的登记在 `opportunity/module.go` |
| 加字段、加接口、改事件 | 装配项目 be-assembly-standard 规范里的“改动配方” | `erp/sales` 读报价请求与写商机参考：改事件要两边一起看 |

## 构建与测试

- `go test ./...`（`make test`）：单元测试：入口的 `contract_test.go`（清单的 `configSchema` 与 `events`、OpenAPI 与路由、合并安全）；生命周期表三项核对；自动赢单的设置。
- `make test-integration`（`go test -tags integration ./itest/`）：真实的 PostgreSQL 与消息系统；每个文件测哪条流程、覆盖哪些规则，在 [itest/AGENTS.md](itest/AGENTS.md)。
- `brickkit lint`

## 设计取舍

- **CRM 与 ERP 只经事件**：请求报价是本组件的事实，销售的回答是它的事件；两边互不声明依赖（都声明会把精确版本互相钉住）。
- **负责人是本组件的字段**：数据范围“销售只看自己的商机”按它；指定别人为负责人要另一个操作。
- **一个变更入口**：每次改商机（用户的命令与销售单据带来的自动赢单）都经 `opportunities.ChangeIn`：锁行、按表的格决定、核对版本、保存、发事件、审计。

## 易错点

- 不要在事务里调主数据：先取证（`internal/masterdata`），再开事务。订阅的处理在 SDK 的事务里，本组件的订阅只写本库，不调用任何组件。
- 不要用浮点算金额：比较与加减在 SQL 里做，按币种的小数位经 `core.Decimal` 与 `money` 舍入。
- 列表与漏斗的筛选值先经 `core.QueryFilters` 校验形式：SDK 在 SQL 里转换类型，形式不对会成为 500。

## 改代码前自查

- 改了契约（`contract/`）：发布 `contract/v…`；OpenAPI 与路由由测试核对；事件的形状 `erp/sales` 也在读（`crm.opportunity.quote_requested.v1`）。
- 改了版本号吗？每次改动都升版本。

<!-- brickkit:managed:begin lang=zh -->
## BrickKit

这是一个 BrickKit 组件：平台只读 `component.yaml`。它依赖的规则：

- `configSchema` 的键就是代码读的环境变量名。不能用保留名：`COMPONENT_ID`、`COMPONENT_VERSION`、`PORT`、`BRICKKIT_SERVED_MEMBERS`、`BRICKKIT_SERVED_MEMBERS_CONFIG`，以及任何 `*_ENDPOINT`。
- 依赖写精确版本。依赖的地址以 `<ID>_ENDPOINT` 注入；缺席的可选依赖根本没有这个变量，读的时候要带兜底。
- `/healthz` 只查本进程，不查依赖。迁移命令用同一个镜像跑，遇到不认识的参数必须直接失败；`local.migrationCommand` 从源码跑同一条迁移，给 `mode: local` 用。
- `BRICKKIT.md` 会随版本进入每个使用它的项目，在那里是脱离仓库单独读的：跟代码一起改，不放相对链接。
- 发版：改 `metadata.version`，提交、推送，`brickkit release`。`brickkit lint` 会检查清单和这些文档——在项目里、在这个目录下跑，只查这个组件（`--all` 查整个项目）。
- 完整规则在 `brickkit-component` 技能里（装了技能的项目或仓库根目录下的 `.claude/skills/brickkit-component/SKILL.md`；`brickkit skills update` 会装上）；参数问 `brickkit <命令> --help`；BrickKit 自己的文档用 `brickkit docs` 看。
<!-- brickkit:managed:end -->
