# crm/opportunity

## 组件定位

销售商机：谁在向哪个客户卖什么、处在哪个阶段、金额多少、概率多大，以及赢单或输单的那一刻。赢单发出一个事件，由 `erp/sales` 转成订单；订单 ID 再经事件带回来，显示在商机上。

**拥有**

- 商机：名称、客户（ID 加一份名称副本）、商机行（产品快照、数量、报价单价、小计）、预计金额、概率（0–100，默认取阶段的值，可手工覆盖）、预计成交日、负责人与部门（建档人 token 的快照）、状态 `OPEN` / `WON` / `LOST` / `CANCELLED`、输单原因、`won_at`，以及 `order_id`（由它转出的订单，订单事件到达前为空串）。
- 阶段定义（迁移播种，没有写接口）与每个商机的阶段流转历史。
- 加权金额（`expected_amount × probability / 100`，读时现算）与按阶段的漏斗。
- 事件 `crm.opportunity.won.v1`、`crm.opportunity.lost.v1`、`crm.opportunity.stage_changed.v1`。

**不拥有**

- 订单、库存预留、应收，以及"赢单"之后的一切：归 `erp/sales`、`erp/inventory`、`erp/finance`。本组件从不调用它们，只保存 `erp/sales` 事件里的订单 ID。
- 客户与产品主数据：归 `mdm/customer`、`mdm/product`。商机行上的单价是报价，不是价目表；订单由 `erp/sales` 自己定价。
- 活动、拜访、线索：归别的 CRM 组件。
- 转化率与预测：归分析类组件，由本组件的事件喂数据。

## 部署前准备

- **PostgreSQL**：schema `crm_opportunity`；如果你的 schema 约定会建 `crm_opportunity_archive` 也可以建（目前没有任务写它）。登录角色 `crm_opportunity_rw`，在 `crm_opportunity` 上有 `USAGE` 与 `CREATE`，以及它的密码。迁移以这个角色运行，建表并写入阶段定义，所以表归它所有——这一点有用：运行中的组件会自己给 `event_outbox` / `event_inbox` 建周分区，这需要表的所有权。brickKit 不建这些；在 BrickEnterprise 装配项目里，`make dev-env` 把密码写进 `.env`，`make db-init` 建 schema、角色与授权。
- **NATS** 可经 `NATS_URL` 访问：事件先写 Outbox 表，再由后台循环推送；`mdm.customer.*` 与 `sales.order.created.v1` 的消费者也在那里订阅。NATS 不可达时组件照常启动，事件留在 Outbox 里等待。
- **`mdm/customer` 与 `mdm/product`** 在运行：建商机时同步地对照它们校验客户与每个产品。
- **权限**（`infra/authz`）与**身份**（`infra/iam-casdoor`，或任何提供 JWKS 的 IAM）可经 `AUTHZ_BUNDLE_URL`、`IAM_JWKS_URL` 访问，REST 路由才会返回错误以外的结果。用户 token 必须带 `sub` 与 `dept_path`：它们成为商机的负责人与部门，决定谁看得见它。它们是配置不是依赖：没有它们组件照样启动。
- 演示数据（可选）：在组件目录 `make seed`，先跑身份、权限、客户、产品的 seed，再以两个身份（超级用户与华东区销售，用来演示部门范围）经真实 REST 接口建 11 个示例商机。其中三个赢单，`erp/sales` 在运行时会收到它们的事件；其中一个刻意超出客户的信用额度，`erp/sales` 会拒绝它的订单。

## 依赖说明

- `mdm/customer`（必需）：建商机时 `BatchGet`，确认客户存在且启用，并复制它的名称。名称副本此后靠 `mdm.customer.created.v1` / `mdm.customer.updated.v1` 保持最新。
- `mdm/product`（必需）：建商机时 `BatchGet`，确认每个产品存在且启用，并把 SKU、名称、基本单位复制到商机行上。

两者都经 gRPC、以调用者的 token 调用，每次调用 3 秒超时；超时答 `504`，依赖不可达答 `503`。不依赖 `erp/sales`：两者之间只有事件（一个方向是 `crm.opportunity.won.v1`，另一个方向是 `sales.order.created.v1`），所以没有 ERP 的项目照样能跑本组件，只是 `order_id` 一直为空。

权限 bundle 与 JWKS 从配置里的 URL 拉取，不是依赖边。没有它们时每条受保护路由都失败关闭：没有或无效的 token → `401`；`IAM_JWKS_URL` 为空或不可达 → `403`；还没拉到过 bundle → `503`；合法用户但没有这个权限键 → `403`。`/healthz` 始终 `200`：它只报告本进程活着。

## 配置指南

| 变量 | 含义 |
|---|---|
| `PG_HOST` | PostgreSQL 主机。通常写项目的共享值（`$var:PG_HOST`）。 |
| `PG_PORT` | PostgreSQL 端口；默认 `5432`。 |
| `PG_DATABASE` | 放 `crm_opportunity` schema 的数据库（`$var:PG_DATABASE`）。 |
| `PG_USER` | 登录角色，字面量 `crm_opportunity_rw`。在外壳里，外壳以自己的角色登录，每个事务切换到这个角色（`SET LOCAL ROLE`），所以角色名必须是 `<PG_SCHEMA>_rw`。 |
| `PG_PASSWORD` | `PG_USER` 的密码。密钥：写 `${CRM_OPPORTUNITY_DB_PASSWORD}`（或你的密钥库引用），永远不写值本身。 |
| `PG_SCHEMA` | 全部表、Outbox 与迁移状态表（`schema_migrations_crm_opportunity`）所在的 schema；默认 `crm_opportunity`。仍然写上字面量，让每个组件的 schema 在一处就能看全。 |
| `NATS_URL` | 发布与消费事件用的 NATS 服务器（`$var:NATS_URL`）。 |
| `OTEL_BASE_URL` | OpenTelemetry collector 的基地址；为空（默认）时不导出。 |
| `AUTHZ_BUNDLE_URL` | 权限判定轮询的 bundle 地址，例如 `http://infra-authz-2-0-0:8223/authz/bundle`。必填：没有它每条受保护路由都答 `503`。要与项目运行的 authz 版本保持一致。 |
| `IAM_JWKS_URL` | 本地验证用户 token 用的 JWKS 地址，例如 `http://infra-iam-casdoor-2-0-0:8200/.well-known/jwks.json`。必填：为空或不可达时每条受保护路由都答 `403`。 |

`mdm/customer` 与 `mdm/product` 的地址由平台按依赖声明注入，不是配置键。

## 契约索引

- `contracts/crm/opportunity/v1/opportunity.proto` — gRPC `crm.opportunity.v1.OpportunityService`，给别的组件用：`CreateOpportunity`、`UpdateOpportunity`（整体替换；`version` 过期 → `ABORTED`）、`ChangeStage`、`MarkWon`、`MarkLost`（都带 `idempotency_key`）、`GetOpportunity`、`ListOpportunities`（游标分页，没有 offset）、`BatchGetOpportunities`（不存在的 ID 进 `missing_ids`，不报错）、`ListStages`。`Opportunity` 带 `order_id`。Go 包是独立模块 `github.com/brickKit/crm-opportunity/gen/crm/opportunity`。
- `contracts/opportunity.openapi.yaml` — REST 在 `/crm/opportunity` 下，每条路由都挂权限键：`GET /opportunities`（`crm.opportunity.view`；`view=mine` 看自己负责的，否则看调用者部门及下级；`customer_id`、`status_filter`、`stage_id_filter`；`created_after` / `created_before`，默认最近 90 天；`cursor`、`page_size`）、`GET /opportunities/{id}`（`crm.opportunity.view`；调用者范围外 `403`）、`GET /opportunities/{id}/stage-history`（`crm.opportunity.view`；调用者范围外 `404`）、`GET /opportunities/funnel`（`crm.opportunity.view`；按阶段给出 `OPEN` 商机的条数、预计金额合计与加权金额合计，`view` 与列表是同一个切换）、`POST /opportunities` 与 `PATCH /opportunities/{id}`（`crm.opportunity.edit`；`version` 过期 → `409`）、`POST /opportunities/{id}/stage` 与 `POST /opportunities/{id}/lose`（`crm.opportunity.edit`）、`POST /opportunities/{id}/win`（`crm.opportunity.win`）、`GET /stages`（`crm.opportunity.view`）。金额是十进制字符串；不合法的金额、数量或概率（`NaN`、`1e3`、负金额、小数位超出列能存的位数）答 `400`。`BatchGetOpportunities` 不在 REST 上。
- `contracts/events/opportunity.events.json` — 经 Outbox、与变更同一个事务发布的事件：`crm.opportunity.won.v1`（`MarkWon` 时；带订单要继承的 `owner_id` 与 `dept_path`）、`crm.opportunity.lost.v1`、`crm.opportunity.stage_changed.v1`。每个都带商机的 `version`。消费：`mdm.customer.created.v1` / `mdm.customer.updated.v1`（客户名称副本），以及 `erp/sales` 的 `sales.order.created.v1`（它的 `source_opportunity_id` 是本组件的商机时回填 `order_id`；商机已关联另一张订单时记日志、不覆盖）。
- `assembly.yaml` — 本项目的元数据：三个权限键 `crm.opportunity.view` / `edit` / `win`、菜单项、边缘路由 `/crm/opportunity/**`、schema 与角色，以及 `opportunities` 上的数据范围 `org`（`dept_path` 前缀）与 `owner`（`owner_id`）。

## 外壳声明

不是外壳。可以由 Go 外壳托管（BrickEnterprise 项目里是 `be/go-backoffice`），也可以单独运行；两种方式代码相同。
