[English](AGENTS.md) · [中文](AGENTS.zh.md)

# crm/opportunity

开发本组件的 AI 指南。用法、边界与契约：BRICKKIT.md；为什么是这个形状：`docs/design.md`；依赖、配置与部署：component.yaml。

## 代码地图

| 路径 | 负责 |
|---|---|
| `backend/module/module.go` | 唯一入口 `New(ctx, rt)`：组装 repo → service → HTTP + gRPC；`Start` 并发跑 Outbox 推送、分区循环与事件消费者。单跑与外壳里是同一个函数 |
| `backend/cmd/server/main.go` | 一行 `besdk.RunStandalone(module.New)` |
| `backend/cmd/migrate/main.go` | 一行 `migrate.Main(migrations.FS)`：迁移容器的入口（`./migrate up`） |
| `backend/internal/repo/repo.go` | 哨兵错误（`ErrInvalidArgument`、`ErrNotFound`、`ErrOpportunityTerminal`、`ErrForbidden`）、`InScope`、幂等辅助函数 |
| `backend/internal/repo/write.go` | 五个写命令，共用一个事务骨架 `runCommand`：占幂等键 → 变更 → Outbox，同一个事务 |
| `backend/internal/repo/opportunity.go` | `Opportunity` 类型、单条读取、`BatchGetOpportunities`、`ListOpportunities`（范围切换、keyset 分页） |
| `backend/internal/repo/decimal.go` | 入参校验（`ValidateAmount`、`ValidateQty`、`ValidateProbability`）与小计、加权金额的精确十进制运算 |
| `backend/internal/repo/funnel.go` | 按阶段的漏斗，在 SQL 里求和 |
| `backend/internal/repo/history.go` | 阶段历史的读取 |
| `backend/internal/repo/order.go` | `BackfillOrderIDTx`：在消费者的事务里回填 `order_id` |
| `backend/internal/repo/stage.go` | 阶段定义（只读） |
| `backend/internal/repo/snapshot.go` | 靠 `mdm.customer.*` 事件维护的客户名称副本 |
| `backend/internal/repo/cursor.go` | 列表游标：`created_at` 与 `id` |
| `backend/internal/service/` | 调任何网络之前的校验、主数据校验、数据范围校验、按错误种类选日志级别，以及错误 → gRPC status 的映射（`status.go`） |
| `backend/internal/client/` | `Deps`：对主数据的两个 gRPC 调用，各带超时 |
| `backend/internal/consumer/` | `mdm.customer.*` 与 `sales.order.created.v1` 的消费者；subject 收在一个结构里，测试可以换成私有名字 |
| `backend/internal/http/http.go` | REST 路由，每条都带权限键注册 |
| `backend/internal/grpc/grpc.go` | `crm.opportunity.v1.OpportunityService` |
| `backend/internal/partition/` | 后台循环，提前四周给 `event_outbox` / `event_inbox` 建周分区 |
| `migrations/` | SQL 迁移（表、阶段播种、`order_id`、历史的概率列），由 `migrations/embed.go` 嵌入 |
| `contracts/` | proto、OpenAPI、事件 schema |
| `gen/crm/opportunity/` | 生成的 Go 代码：独立的嵌套 Go 模块，有自己的 tag；永远不手改 |
| `scripts/` | 本地演示数据的 `seed.sh` / `seed-clean.sh` |

| 要做的事 | 从这里开始 | 然后 |
|---|---|---|
| 给写命令加规则 | `backend/internal/repo/write.go` | `backend/internal/service/service.go`（网络调用之前的校验）、`backend/internal/repo/write_test.go` |
| 新的数字类入参 | `backend/internal/repo/decimal.go`（一条严格的正则） | `backend/internal/repo/validate_test.go` |
| 新的 REST 路由 | `backend/internal/http/http.go`（`besdk.GET` / `POST` / `PATCH` 加 `assembly.yaml` 里的权限键） | `contracts/opportunity.openapi.yaml`；挨着 `:id` 的静态路径看 `backend/internal/http/routes_test.go` |
| 仪表盘上的一个数 | `backend/internal/repo/funnel.go` | `backend/internal/service/funnel_test.go`（两个用户、两个部门） |
| 响应别的组件的事件 | `backend/internal/consumer/consumer.go` | `backend/internal/repo/order.go`、`backend/internal/consumer/order_test.go` |
| 给商机加字段 | `migrations/`（新的迁移） | `contracts/`（只增）、`buf generate`、`backend/internal/repo/opportunity.go` |
| 某个错误回的状态码不对 | `backend/internal/service/status.go` | `backend/internal/repo/repo.go` 里的哨兵错误 |

## 构建与测试

```bash
# 测试连测试库 brickkit_test_db，绝不连 brickkit_db（项目根 make test-db-init ID=crm/opportunity）
set -a; . ../../../.env; set +a
export TEST_PG_DSN="postgres://postgres:${POSTGRES_PASSWORD}@localhost:5432/brickkit_test_db?sslmode=disable"
make test                    # 每个包都以 "ok" 结尾；没设 TEST_PG_DSN 时拒绝运行
go test ./... -count=1 -v | grep -- '--- SKIP'   # 只有 backend/internal/service/service_test.go 里的两条跨组件测试
make check-version dag-check contract-check import-scan module-check   # 各打印一行 ✓
make docs-check              # "0 with errors, 0 warnings"
```

两条跳过的测试要 `mdm/customer` 与 `mdm/product` 在跑；在项目根 `make test-cross ID=crm/opportunity` 把它们指到正在跑的容器上，在那里必须通过。迁移：项目根 `make test-db-init ID=crm/opportunity` 以 `crm_opportunity_rw` 连跑两次（`✓ 迁移幂等`）。`buf generate` 之后（契约变了）契约包要打新 tag：见易错点。真机上，在项目根：`make verify ID=crm/opportunity ROUTE=/crm/opportunity/opportunities FOCUS=1` 构建镜像、只起本组件需要的东西、核对迁移、健康检查与权限判定、跑一次 focus，最后拆掉。

## 设计取舍

- **CRM 与 ERP 之间只有事件。** 赢单发 `crm.opportunity.won.v1`；订单 ID 经 `sales.order.created.v1` 回来。这里没有任何对 `erp/*` 的调用。
- **两条同步边，都指向主数据**，只在建商机时用、以调用者的 token 调，所以坏的客户与产品在写入任何东西之前就被挡住。
- **每个写命令都幂等，并与它的事件一起提交**（`runCommand`）：提交了的变更一定发事件，回滚了的一定不发。
- **数字用正则校验，不靠数据库：** `NUMERIC` 会存下 `NaN`，多出的小数位会被悄悄四舍五入。
- **加权金额现算、从不落库**；所有金额运算都按十进制。
- **两个数据范围维度 `org` 与 `owner`，与 `erp/sales` 相同。** 列表与漏斗用 `view` 在两者之间切换；单条读取两者满足其一即可。
- **`order_id` 只写一次。** 第二张订单声称来自同一个商机时记 `ERROR`，不生效。

## 易错点

| 绝不 | 症状 | 原因 |
|---|---|---|
| 把 `erp/sales`（或任何 `erp/*`、`infra/workflow`）加进 `dependencies.components`，或在 `MarkWon` 里调它 | 生意确实赢了，赢单却因为库存不足失败；只装 CRM 的项目装不起来 | CRM 与 ERP 之间只走事件；`make dag-check` 遇到这种边就失败 |
| 用 `strconv.ParseFloat` 校验金额，或不校验直接交给 SQL | `NaN` 被存下，漏斗合计变成 `NaN`；`1e3`、`100.005` 被悄悄接受；`abc` 答 `500` | 用 `backend/internal/repo/decimal.go` 里的校验函数；`backend/internal/repo/validate_test.go` 列出了这些写法 |
| 在列表或漏斗上写 `owner_id = 我 OR dept_path LIKE …` | `view=mine` 显示的是整个部门的数 | 两种视角由 `view` 切换；`backend/internal/service/funnel_test.go` 用两个部门的两个用户核对 |
| 在消费者里覆盖非空的 `order_id` | 第一张订单悄悄和商机断了联系 | 一个商机只转一次单；冲突是要人去看的数据问题 |
| 从赢单事件里去掉 `owner_id` 或 `dept_path` | `erp/sales` 照样建出订单，但谁的列表里都看不到 | 订单从事件里继承商机的归属；那个 handler 里没有用户 token |
| 在 repo 的事务里调 `mdm/customer` / `mdm/product` | 依赖一慢，行锁就被占上几秒，别的写入排队 | service 先调它们；repo 从不发网络调用 |
| 在消费者测试里往生产 subject 发布 | 组件容器在跑时随机失败：容器消费掉测试消息、写进演示库 | 用测试私有 subject 启动消费者（`start(…, subjects{…})`） |
| 改 `contracts/crm/opportunity/v1/opportunity.proto` 的注释或选项却不给契约包打 tag | 本地构建照过（`replace` 把问题盖住了）；拉 `crm-opportunity/v2` 的外壳对着旧的 `gen/` 编译 | `gen/crm/opportunity/` 下任何变化都要打新的契约包 tag，并让根 `go.mod` require 它 |

## 改代码前自查

1. 这件事是关于商机（这里）、订单（`erp/sales`）还是客户（`mdm/customer`）？只有第一种放这里。
2. 我是不是在加一条到 ERP 的同步边，或者读 ERP 的状态？停：改成发布或消费事件（见 `docs/design.md`）。
3. 改契约？只增：新字段、新 rpc、新路径。绝不删或改类型：字段、rpc、路径、事件 subject 都一样。
4. `gen/` 变了吗？那契约包要打新 tag，`go.mod` 要 require 它。
5. 新 REST 路由用 `besdk.GET` / `POST` / `PATCH` 加 `assembly.yaml` 里的权限键注册；有数据范围的读接口要有真实建库的测试，两个用户互相看不到对方的行。
6. 新规则 → 先写会失败的测试（涉及 SQL 的用真库），再写代码。
7. 发布之后第一次改动前先升 `metadata.version`；BRICKKIT.md 与 `docs/design.md` 与代码同一个提交更新。
