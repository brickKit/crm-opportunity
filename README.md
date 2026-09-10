# crm-opportunity · 商机管理

商机全生命周期、漏斗与赢单预测原始数据、赢单事件——阶段三**第一个业务组件**，也是附录 E 那条"商机赢单→建订单"链路的**起点**。⚠️ 规范源里最要紧的一条是设计书 §1.4：**CRM 与 ERP 零同步边，仅事件握手**——本组件对 `erp-sales`/`erp-inventory`/`erp-finance`/`infra-workflow` 零依赖边，赢单只广播事件，从不同步调用。

## 它能做什么

- `crm.opportunity.v1.OpportunityService`（gRPC，组件间协议）：`CreateOpportunity`/`UpdateOpportunity`/`ChangeStage`/`MarkWon`/`MarkLost` 五个命令全部 claim-first 幂等；`GetOpportunity`/`BatchGetOpportunities`（防 N+1）/`ListOpportunities`/`ListStages` 四个读接口
- REST（人类操作，`/crm/opportunity/**`）：`POST /opportunities`（建档）、`GET /opportunities`（漏斗视图数据源，`?view=mine|dept` 切两个视角）、`PATCH /opportunities/{id}`、`POST /opportunities/{id}/stage`（阶段推进）、`POST /opportunities/{id}/win`（⭐ 链路的扳机，单独权限键 `crm.opportunity.win`）、`POST /opportunities/{id}/lose`、`GET /stages`
- `MarkWon` 成功提交后，经 Outbox 发布 `crm.opportunity.won.v1`——⭐ 全系统第一条真正跨 CRM/ERP 域界的核心事件，`owner_id`/`dept_path` 两个字段最容易漏（事件 handler 跑在系统身份下，没有 JWT，归属必须靠事件带）
- 阶段（初步接洽→需求确认→方案报价→商务谈判→赢单）是数据不是代码，迁移播种；本阶段任意阶段之间可以自由跳转，只记历史，不做审批/顺序约束（Fork 点，见设计文档 §8）

## 需要哪些基础资源

| 资源 | 形态 | 为什么需要 | 怎么起 |
|---|---|---|---|
| PostgreSQL 16 | **A**（`kind: database`） | `opportunities`/`opportunity_items`/`opportunity_stage_history`/`opportunity_stages`/`customer_snapshots` 独占 schema `crm_opportunity` | 装配仓库根目录 `make up` |
| NATS 2.10 | **A**（`kind: mq`） | 发布 `crm.opportunity.*.v1` 三条事件；消费 `mdm.customer.created.v1`/`.updated.v1`（维护展示快照） | 同上 |

⚠️ 本组件**不需要** Traefik 与 Casdoor 就能单独跑起来——它不对 IAM 建依赖边，JWT 走本地验签（决策 87）。

⚠️ 两条强依赖：`mdm/customer`、`mdm/product`（建商机/商机行时 `BatchGet` 校验客户与产品是否存在、取展示快照——建档那一刻就要挡住脏数据）。`brickkit up` 会按拓扑顺序处理，单独跑本组件做开发时记得这两个也要在。

## 怎么起来

```bash
# 装配仓库根目录
make up                        # 起 PostgreSQL/NATS 等默认基础资源
cd components/crm/opportunity
go build -o build/migrate ./backend/cmd/migrate
PG_SCHEMA=crm_opportunity DATABASE_HOST=localhost DATABASE_PORT=5432 \
  DATABASE_USER=postgres DATABASE_PASSWORD=<.env 里的 POSTGRES_PASSWORD> DATABASE_NAME=brickkit_db \
  ./build/migrate up
go run ./backend/cmd/server     # 单独跑：besdk.RunStandalone 读 component.yaml 的端口
```

或者用平台：`brickkit up`（装配仓库根目录，`components/crm/opportunity` 登记为 submodule 且在 `brickkit.yaml` 里之后）。

## 怎么用

```bash
# 建一个商机（gRPC）——items[].quoted_unit_price 是报价快照，不是权威价目
grpcurl -plaintext -d '{
  "idempotency_key": "opp-demo-1",
  "name": "示例客户 Q4 采购意向",
  "customer_id": "1",
  "items": [{"product_id": "1", "qty": "10", "quoted_unit_price": "888.00"}],
  "expected_amount": "8880.00"
}' localhost:9102 crm.opportunity.v1.OpportunityService/CreateOpportunity

# 前端看漏斗（REST，人类操作）
curl -H 'Authorization: Bearer <应用 token>' 'http://localhost:8102/crm/opportunity/opportunities?view=dept'

# 阶段推进
curl -X POST -H 'Authorization: Bearer <应用 token>' -H 'Content-Type: application/json' \
  -d '{"idempotency_key":"stage-1","to_stage_id":"3"}' \
  http://localhost:8102/crm/opportunity/opportunities/1/stage

# ⭐ 赢单——触发链路扳机，发布 crm.opportunity.won.v1
curl -X POST -H 'Authorization: Bearer <应用 token>' -H 'Content-Type: application/json' \
  -d '{"idempotency_key":"win-1"}' \
  http://localhost:8102/crm/opportunity/opportunities/1/win
```

## 配置项

| 配置键 | 默认值 | 说明 |
|---|---|---|
| `pgSchema` | `crm_opportunity` | 本组件的 PG schema |
| `otelBaseUrl` | `""` | 空 = Blackhole Exporter，零成本 |
| `iamJwksUrl` | `""` | JWT 本地验签的公钥来源，指向 `infra-iam-casdoor` |
| `authzBundleUrl` | `""` | 权限判定的 bundle 轮询地址，指向 `infra-authz` |

## 参考实现

| 项目 | 看的模块 | 借鉴了什么 | 许可证（已复核） | 用法 |
|---|---|---|---|---|
| Odoo 17.0 | `addons/crm/models/crm_lead.py`、`crm_stage.py` | `probability`(0–100，阶段带默认值+允许手工覆盖)、`expected_revenue`、加权=两者相乘不落库、失单必须记 `loss reason`；阶段存表不写死 | LGPL-3 | 借鉴逻辑 |
| ERPNext | `Opportunity` doctype | 商机行的快照字段构成 | GPL-3 | 借鉴逻辑 |
| SuiteCRM / Salesforce | — | 概率与阶段绑定的默认值、漏斗阶段模型 | 闭源 | 借鉴实际应用 |

**要避免它的什么**：Odoo 的 `probability=100` 即视为赢单自动关闭（拖进"Won"看板列就顺手建单）——本组件**不学**，赢单是显式的 `MarkWon` 动作，一个概率数字不该顺手触发建单+锁库存+生成应收这条链路。多数 CRM 在商机上挂"已转订单"状态字段——本组件不这么做，那要么同步查 ERP（违反 §1.4），要么反向消费 ERP 事件（让 CRM 依赖 ERP 状态机），正确做法是前端/BFF 分别查两边再聚合展示。

## 边界与禁令

- **CRM 与 ERP 零同步边，仅事件握手**（§1.4 铁律）——本组件被 `frontend-standard`/`infra-bff-mobile` 读取，未来还会被 `crm-*` 其它组件依赖，绝不能因为附录 E 的链路"看起来"该建一条同步边就顺手加上
- 两条依赖边全部用 `besdk.UserClient`（透传 JWT），**不许用 `SystemClient`**——这是用户请求路径，用 `SystemClient` 会绕过下游数据权限
- `crm.opportunity.won.v1` 的 `owner_id`/`dept_path` **必须带**——`erp-sales` 的事件 handler 跑在系统身份下没有 JWT，漏了这两个字段订单会建出来但没有归属，且不报任何错
- 商机行的 `quoted_unit_price` 是**报价快照供参考**，不是权威价目——`erp-sales` 赢单转订单时会用自己的定价引擎重算，两者允许不一致
- `opportunities`/`opportunity_items`/`opportunity_stage_history` **不分区**——量级比交易流水低一到两个数量级（跟随 §11.2.5 分区大表清单，那张表里没有本组件）
- 不消费 `erp-sales` 的任何事件——赢单之后订单建成没有、发货没有，本组件一概不追踪
- 阶段推进不做审批/顺序约束——这是刻意的 Fork 点，不是遗漏（客户需要更严格的流程管控时走定制）
