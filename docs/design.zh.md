[English](design.md) · [中文](design.zh.md)

# crm/opportunity 设计

只写结论，给要改本组件设计的人看。怎么用：`BRICKKIT.md`；代码怎么组织：`AGENTS.md`。

## 边界

本组件拥有销售商机：谁在向哪个客户卖什么、处在哪个阶段、金额多少、概率多大，以及赢单或输单的那一刻。赢单是订单链路的扳机：本组件宣布这个事实，不关心后面发生什么。

| 不在这里 | 归谁 | 为什么 |
|---|---|---|
| 订单、库存预留、应收 | `erp/sales`、`erp/inventory`、`erp/finance` | "赢单"之后的一切都归 ERP。订单建没建成、库存够不够、有没有跑补偿：本组件不知道，也不该知道 |
| 客户主数据 | `mdm/customer` | 本组件只存 `customer_id` 加一份名称的展示副本 |
| 产品与价格 | `mdm/product`、`erp/sales` | 商机行上的单价是谈判时的报价，不是价目表；`erp/sales` 用自己的定价引擎给订单定价，两者可以不一致 |
| 活动、拜访、电话 | 将来的 CRM 活动组件 | 另一个聚合根；`opportunities` 上没有 `last_contact_note` 这类字段 |
| 线索 | 将来的 CRM 线索组件 | 线索转商机发生在那边 |

本组件保存的唯一一项订单信息是 `order_id`：由赢单商机转出的那张订单的 ID。它只经消费 `erp/sales` 自己的事件写入（见"发布与消费的事件"），从不去问 ERP，所以 CRM 到 ERP 仍然没有同步边，也不跟踪订单的状态机（已确认、已发货、已取消都留在 `erp/sales`）。

## 拥有的数据

| 表 | 分区 | 说明 |
|---|---|---|
| `opportunities` | 否 | `customer_id` + 展示用 `customer_name`；`owner_id`、`dept_id`、`dept_path` 是建档时从建档人的 token 取的快照；`stage_id`；`expected_amount NUMERIC(18,2)`；`probability` 0–100；`expected_close_date`；`status`；`currency`（单币种，默认 `CNY`）；`lost_reason`；`won_at`；`order_id`（订单事件到达前为空串）；`version` 乐观锁 |
| `opportunity_items` | 否 | 产品、SKU、名称与单位的快照，`qty NUMERIC(18,6)`，`quoted_unit_price`，写入时算好的 `subtotal` |
| `opportunity_stage_history` | 否 | 只追加：从哪个阶段、到哪个阶段、这次流转设定的概率、谁、何时。`probability` 列加上之前写下的行，这一列是 `NULL` |
| `opportunity_stages` | 否 | 迁移播种的阶段定义（`INITIAL_CONTACT` 10 %、`NEEDS_CONFIRMED` 30 %、`PROPOSAL` 50 %、`NEGOTIATION` 70 %、`WON` 100 %）；阶段是数据不是代码，没有写接口 |
| `customer_snapshots` | 否 | 客户名称，靠 `mdm.customer.*` 事件保持最新 |
| `command_idempotency` | 否 | 主键 `idempotency_key`，记第一次执行产生的商机 ID |
| `event_outbox`、`event_inbox` | 按 `created_at` 周分区 | 组件自己的循环提前四周建分区；没有它，第五周起插入失败，报"no partition of relation found for row" |

终态：`WON`、`LOST`、`CANCELLED`。活动态：`OPEN`。只有 `OPEN` 能改、能换阶段、能赢单或输单。

加权金额（`expected_amount × probability / 100`）读时现算，从不落库：存下来的值在金额或概率一变就漂移。所有金额运算都按十进制（精确有理数，四舍五入到分、.5 远离零，与 PostgreSQL 的 `round(numeric, 2)` 同一条规则），从不经过 `float64`。

## 契约面

gRPC `crm.opportunity.v1.OpportunityService`，给别的组件用：

| rpc | 类型 | 说明 |
|---|---|---|
| `CreateOpportunity` | 写 | 幂等；对照主数据校验客户与产品；从 `sort_order` 最小的阶段开始；概率取阶段默认值，除非调用方给了（`optional`，所以 0 是合法的覆盖值） |
| `UpdateOpportunity` | 写 | 幂等；`name` 与 `expected_amount` 必带（空的 → `INVALID_ARGUMENT`）；没带 `owner_id` 或 `expected_close_date` 就保留原值；`version` 过期 → `ABORTED`。这两个可选字段没带时保留而不是清空，是因为照契约只带改动字段的调用方不能把负责人抹掉：没有负责人，商机就从 `view=mine` 里消失，之后赢单事件带 `owner_id: ""`，没有部门的人建的商机（`dept_path` 为空）从此谁都看不到。代价是预计成交日一旦设了，就不能经这个调用清空 |
| `ChangeStage` | 写 | 幂等；任意阶段之间都能跳，写一行历史 |
| `MarkWon` | 写 | 幂等；置 `WON`、概率 100、`won_at`，并在同一个事务里写 `crm.opportunity.won.v1` |
| `MarkLost` | 写 | 幂等；必须给原因 |
| `GetOpportunity`、`ListOpportunities` | 读 | `List` 用游标分页；`created_after` / `created_before` 给出建档时间窗口，默认最近 90 天 |
| `BatchGetOpportunities` | 读 | 调用方一次取多条，代替循环 `Get`；不存在的 ID 进 `missing_ids` |
| `ListStages` | 读 | 阶段定义 |

`Opportunity` 带 `order_id`（回填前为空串）。

**数字类入参在写库、调依赖之前用正则校验**：`expected_amount` 与 `quoted_unit_price` 是非负的普通十进制数，最多 16 位整数、2 位小数（`NUMERIC(18,2)`）；`qty` 是正的普通十进制数，最多 12 位整数、6 位小数（`NUMERIC(18,6)`）；手工覆盖的概率是 0–100。其余一律 `InvalidArgument`（`400`），`NaN`、`Inf`、`1e3`、`-1`、`.5`、`100.005` 都在内。不能靠数据库来挡：PostgreSQL 的 `NUMERIC` 会把 `'NaN'` 原样存下（它比任何数都大，`CHECK` 拦不住，漏斗合计整列变成 `NaN`），认指数写法，超出的小数位悄悄四舍五入，溢出或违反 `CHECK` 报的是服务端错误（`500`）。`strconv.ParseFloat` 同样认 `NaN`、`Inf` 和指数写法。

REST 在 `/crm/opportunity` 下：

| 路由 | 权限键 |
|---|---|
| `GET /opportunities`、`GET /opportunities/{id}` | `crm.opportunity.view` |
| `GET /opportunities/funnel`、`GET /opportunities/{id}/stage-history` | `crm.opportunity.view` |
| `POST /opportunities`、`PATCH /opportunities/{id}` | `crm.opportunity.edit` |
| `POST /opportunities/{id}/stage`、`POST /opportunities/{id}/lose` | `crm.opportunity.edit` |
| `POST /opportunities/{id}/win` | `crm.opportunity.win` |
| `GET /stages` | `crm.opportunity.view` |

赢单有自己的权限键：它会触发建单、库存预留和应收，是这里唯一在下游有财务后果的动作。`BatchGetOpportunities` 只在 gRPC 上。

**REST 的错误**按 SDK 的 gRPC → HTTP 映射：参数不合法（含列表的 `created_after` / `created_before` 格式不对）与对终态商机做操作 → `400`；各写路由上，商机不在调用者的数据范围内 → `403`；不存在，或 `GET /opportunities/{id}` 与 `stage-history` 上商机不在调用者的数据范围内 → `404`；`PATCH` 的 `version` 过期 → `409`（`ABORTED`，OpenAPI 契约里有声明）；主数据调用超时 → `504`。调用方能自己纠正的错误记 `INFO`，超时与依赖不可用记 `WARN`，只有内部错误记 `ERROR`。

**阶段历史**（`GET /opportunities/{id}/stage-history`）：一个商机的每次阶段流转，按时间先后，`{from_stage_id, to_stage_id, probability, changed_by, changed_at}`。不在调用者数据范围内的商机答 `404`，和不存在完全一样，这条路由不泄露哪些 ID 存在。不分页：它是一个商机的子集合，长度受一单生意换阶段的次数限制。建档本身不算一行历史（没有"从哪来"）。

**漏斗**（`GET /opportunities/funnel`）：按 `sort_order` 每个阶段一条记录，给出 `OPEN` 商机的条数、`expected_amount` 合计与加权金额合计；每个阶段都列出，没有商机的阶段为 0。加权合计是每条商机的加权金额先四舍五入到分再求和，所以等于列表逐条显示的值相加。`view` 与列表是同一个切换：`mine` 只数调用者自己负责的（`owner_id` = 调用者），其余取调用者的部门及下级（`dept_path` 前缀）。是切换，不是 `OR`。不加时间窗口：销售管道就是所有还开着的单子，不论多久以前建的。

没有给超时写入用的状态查询端点：没有组件同步地往这里写。

## 发布与消费的事件

发布，全部经 Outbox、与状态变更在同一个事务里：

| Subject | 何时 | Payload |
|---|---|---|
| `crm.opportunity.won.v1` | `MarkWon` | `opportunity_id`、`customer_id`、`items[]`（`product_id`、`quantity`、`quoted_unit_price`）、`owner_id`、`dept_path`、`currency`、`expected_close_date`、`revision` |
| `crm.opportunity.lost.v1` | `MarkLost` | `opportunity_id`、`reason`、`amount` |
| `crm.opportunity.stage_changed.v1` | `ChangeStage` | `opportunity_id`、`from_stage`、`to_stage` |

赢单事件上的 `owner_id` 与 `dept_path` 是 `erp/sales` 拿来当订单负责人与部门的：它在事件 handler 里建单，手里没有用户 token，订单继承商机的归属。缺了它们，订单照样存在，只是谁的列表里都看不到。`quoted_unit_price` 只供参考。信封的 `version` 是变更之后商机的 `version`。

消费：

| Subject | 来自 | 做什么 | 重复与乱序 |
|---|---|---|---|
| `mdm.customer.created.v1`、`mdm.customer.updated.v1` | `mdm/customer` | 刷新 `customer_snapshots.name` | 只在事件的 version 大于已存的值时生效 |
| `sales.order.created.v1` | `erp/sales` | 回填 `order_id` | 见下 |

**`order_id` 回填。** `erp/sales` 在订单确认时发 `sales.order.created.v1`，带一个可选的 `source_opportunity_id`（不是由商机转来的订单为空或不带）。消费者只宽容地读 `order_id` 与 `source_opportunity_id`：字段缺失就是空串，`source_opportunity_id` 为空表示"不是我们的"、事件忽略，不是本组件商机的 ID 同样忽略。否则，与 inbox 行在同一个事务里：

- `order_id` 为空 → 写入（version 与 `updated_at` 前移，不发事件）；
- `order_id` 已经是同一张 → 什么都不做（重复投递或重放）；
- `order_id` 已经是另一张订单 → 记 `ERROR`，不覆盖：一个商机只转一次单，第二张订单声称来自同一个商机是要人去看的数据问题，不能悄悄处理掉。

同一个订单事件的重复投递也被 inbox（subject、订单 ID、version）去掉。这个 subject 缺了 `<domain>.<aggregate>.<action>` 要求的 `erp.` 域前缀；按发布时的名字保留，因为改 subject 名就等于删掉一个 subject。

## 依赖

| 依赖 | 用来做什么 |
|---|---|
| `mdm/customer` | 建商机时 `BatchGet`：客户存在且 `ACTIVE`，并取名称做快照 |
| `mdm/product` | 建商机时 `BatchGet`：每个产品都存在且 `ACTIVE`，并取 SKU、名称、基本单位做行快照 |

两者都以调用者的 token 调用（`UserClient`），从不以组件自己的身份，每次调用 3 秒超时。依赖边是同步的，原因就是要在建档时挡住脏数据：事后对账撤不回一个建在不存在的客户上的商机。

预期中有、实际没有：

| 不是依赖 | 为什么 |
|---|---|
| `erp/sales` | CRM 与 ERP 之间没有同步边，只有事件。`MarkWon` 里同步"建订单"会让赢单在库存不足时失败，尽管生意确实赢了；只装 CRM 的项目也装不起来；订单还会变成商机事务里的一步。订单 ID 改由事件带回来 |
| `erp/inventory`、`erp/finance` | 更远：商机不知道库存或应收的存在 |
| `infra/workflow` | 商机不走审批。将来若有"大单赢单要审批"这类规则，是本组件去调工作流，不是反过来 |
| `infra/iam-casdoor`、`infra/authz` | token 按 `IAM_JWKS_URL` 本地验签，权限来自 `AUTHZ_BUNDLE_URL` 的 bundle：是配置，不是依赖边 |

## 在同步调用图里的位置

两条出边（`mdm/customer`、`mdm/product`），都指向不调任何人的主数据，所以从这里出发的每条路径长度都是一，不会成环。入边：前端与移动端 BFF 经 REST 调用。没有组件同步调用它；`erp/sales` 只经 `crm.opportunity.won.v1` 听到它，它也只经 `sales.order.created.v1` 听到 `erp/sales`。

## 分区与归档

`opportunities`、`opportunity_items`、`opportunity_stage_history` 不分区：一个销售机会一行，比交易流水少一到两个数量级。`event_outbox` 与 `event_inbox` 按周分区。

归档规则：终态商机（`WON`、`LOST`、`CANCELLED`）超过 24 个月连同行与历史移入 `<PG_SCHEMA>_archive`；`OPEN` 商机不论多旧都留在热数据里。窗口比订单流水长，因为销售分析要拿一个季度和去年同一季度比。`opportunity_stages` 与 `customer_snapshots` 从不归档。

## 数据库身份

组件不认识任何数据库名字。schema 是 `PG_SCHEMA`，每个事务切换到的角色（`SET LOCAL ROLE`）是 `PG_USER`，两个都来自配置；迁移里两个都不写，不带限定名的表名经迁移入口设好的 search_path 解析。单跑时 `PG_USER` 就是连接池登录的角色；在外壳里，连接池以外壳的身份登录，每个成员切换到自己的 `PG_USER`。两个键缺一个、或者不是普通的小写标识符，`module.New` 就拒绝启动：名字配错了在启动时失败，而不是第一个请求才失败。

以前角色由 schema 推出（`<PG_SCHEMA>_rw`），迁移在每个 `CREATE TABLE` 后面写 `OWNER TO crm_opportunity_rw`。这样 `PG_SCHEMA` 只是名义上可配：换一个 schema，切换到的角色不存在；换一个登录角色，迁移在写死的属主上失败。两处错的是设计，不只是代码：组件必须能部署进部署者给的任何 schema 和角色，数据库必须可以更换。已经执行过的 001、002 两个迁移删掉了 `OWNER TO`；这对已有的库没有任何影响：执行过的迁移不会再跑，库里的表本来就归那个角色（`make test-db-init` 前后，测试库与演示库的属主都没有变）。`backend/module/identity_test.go` 证明这一点：在测试库里建一个名字互不相干的 schema 和登录角色，以这个角色迁移，核对每张表都归它，再经 `module.New` 让后台循环建分区、写一条商机再读回来，单跑与外壳成员两种形态都走一遍。

测试守同一条规则：schema 与角色来自 `backend/testdb`（`TEST_PG_SCHEMA` / `TEST_PG_ROLE`，默认是 `make test-db-init` 建的那一套）；种子脚本从项目的 `config/` 读每个组件的 schema。

## 数据范围

`opportunities` 上两个维度：`org`（`dept_path` 前缀）与 `owner`（`owner_id` 相等）。与 `erp/sales` 是同样的两个维度，刻意如此：订单继承商机的归属，两边维度对不上，就会出现"商机我看得见、转出来的订单我看不见"。

| 路由 | 范围怎么生效 |
|---|---|
| `GET /opportunities`、`GET /opportunities/funnel` | `view=mine` → `owner_id` = 调用者的 `sub`；否则 `dept_path` 以调用者的 `dept_path` 开头。是切换：一次只用一个操作数，两个都来自调用者的 token |
| `GET /opportunities/{id}`、`GET /opportunities/{id}/stage-history` | `dept_path` 以调用者的 `dept_path` 开头，**或** `owner_id` 是调用者，即可见：两个操作数都来自调用者的 token；范围外 → `404`，与不存在的 ID 完全一样 |
| 各写路由（`PATCH`、`stage`、`win`、`lose`） | 与单条读取的可见性相同；范围外 → `403`：要写的人本来就拿着这个 ID |

**没有部门的调用者。** 真实的部门路径恒为 `/<id>/…/`，根部门也是 `/<根id>/`。token 里 `dept_path` 为空只有一种含义：这个人还没被分到任何部门。SDK（be-sdk-go v0.5.0）这时给出一个什么都不匹配的前缀（`besdk.NoDeptPath`）和 `HasDept = false`，于是 `org` 维不命中任何行：列表的部门视图为空，部门漏斗为零，单条读取与写命令只剩"我是负责人"这一个理由。要看全公司，就分到根部门；`"/"` 是覆盖所有部门树的显式标记，作为普通前缀匹配每一条有部门的商机。

这样的人仍然可以建商机。归属快照这时把 `dept_id` 与 `dept_path` 写成空串，绝不写 SDK 的那个标记：`dept_path` 为空的行不属于任何部门，只有负责人看得到，`"/"` 与根部门都不命中它。赢单事件带着这个空的 `dept_path`，由它转出的订单同样只有负责人看得到。

仓储层也不把这件事交给调用方：`InScope` 收到空前缀时部门一侧不命中，列表或漏斗的部门视角收到空前缀是 `InvalidArgument`，不是"不限"。空前缀只可能是调用方漏填了。

## 参考实现

| 项目 | 版本 | 参考的模块 | 借鉴了什么 | 许可 | 用法 |
|---|---|---|---|---|---|
| Odoo | 17.0 | `addons/crm/models/crm_lead.py`、`crm_stage.py` | `probability` 0–100，默认取阶段的值、可手工覆盖；预计收入；加权金额是乘积、不落库；输单必须给原因；阶段存为数据 | LGPL-3 | 借鉴思路 |
| ERPNext | — | `Opportunity` doctype | 商机行带哪些快照字段 | GPL-3 | 借鉴思路 |
| Salesforce、SuiteCRM | — | 商机阶段与预测 | 概率作为默认值绑定在阶段上 | 专有 | 观察行为 |

**刻意避开的**：Odoo 在概率到 100 时把商机关成赢单：这里赢单是显式的 `MarkWon`，因为它会触发建单，表单里的一个数字不该做这件事。Odoo 把线索和商机放在一张表：两者的生命周期与数据范围不同。CRM 侧的"已转化 / 已开票"状态：那意味着同步问 ERP 或跟踪它的状态机；这里只保存订单 ID，订单状态留在 `erp/sales`。

产品之间两处真实的分歧，都定为客户 fork 而不是槽位，因为本组件会被别的 CRM 组件依赖，而且第一处分歧也存在于 `erp/sales`：赢单商机是否自动转订单（这里：自动），阶段之间要求多严格的先后顺序（这里：任意阶段到任意阶段）。

## 未决问题

| 问题 | 当前答案 |
|---|---|
| `order_id` 从 `sales.order.created.v1` 回填，而 `erp/sales` 在订单确认时才发它：停在草稿的订单（例如超出信用额度）让 `order_id` 一直为空 | 暂时接受："已转化"意味着"存在一张已确认的订单"。如果界面也要看草稿订单，由 `erp/sales` 增加一个草稿已建的事件，本消费者再订阅它 |
| 没有部门的人建的商机，在他被分到部门之后 `dept_path` 仍是空串 | 接受：部门是建档时的快照，没有任何记录说明这一行"本该"属于哪个部门。要紧的话由人手工改负责人（`PATCH`）；部门列仍是快照 |
| gRPC 服务端没有 token 验签：需要调用者数据范围的读写经 gRPC 会失败，`BatchGetOpportunities` 不加任何范围。发出的调用也不带 token：`besdk.UserClient` 只透传经 gRPC 进来的 `authorization`，REST 请求没有它 | SDK 还没有 gRPC 认证拦截器；面向用户的是 REST。在 SDK 里补，不在这里补 |
| 还没有归档终态商机的任务 | 上面的规则不变；任务随项目的归档工作一起来 |
| 漏斗转化率与预测 | 这里只有各阶段的合计。转化分析归分析类组件，由 `crm.opportunity.stage_changed.v1` 喂数据，不归业务组件 |
| 客户在主数据里还不存在的商机（潜在客户） | 不支持：客户必须先存在。潜在客户归将来的 CRM 客户组件 |
