# crm-opportunity · AI 助手导读

## 身份证

| 项 | 值 |
|---|---|
| 组件 ID | `crm/opportunity` |
| 仓库名 | `crm-opportunity` |
| 端口 | HTTP `8102` / gRPC `9102`（`registry/ports.tsv`，装配仓库根目录那份） |
| schema / role | `crm_opportunity` / `crm_opportunity_rw`（归档 schema `crm_opportunity_archive`，24 个月归档窗口，见设计计划 §7） |
| 语言 / 框架 | Go：Gin + `database/sql` + `pgx/v5/stdlib` + `golang-migrate` |
| 合并部署时进 | 外壳二 `go-backoffice` |
| 装配角色 | `optional` |
| 设计真相源 | 装配仓库 `docs/design/crm-opportunity.md`——本文件与它冲突时，以那份为准，回来改这里 |

## 边界

**归我：** 商机全生命周期（建档、阶段推进、赢单/输单及其历史）、商机行（打算卖哪些产品/多少量/什么价，快照非权威）、漏斗与赢单预测所需的原始数据（阶段、金额、概率、预计成交日）、赢单时发出那条事件——链路的扳机。

**不归我：**

| 什么 | 归谁 | 为什么 |
|---|---|---|
| 订单 | `erp-sales` | 赢单之后的事全归它。我发完事件就不再关心后续——订单建没建成、库存够不够、要不要补偿，一概不知道也不该知道 |
| 客户主数据 | `mdm-customer` | 我持 `customer_id` 与展示快照 |
| 产品与价格 | `mdm-product` / `erp-sales` | 商机行上的单价是报价快照，不是权威价目表 |
| 跟进记录、拜访、通话 | `crm-activity`（阶段六） | 本阶段不做。⚠️ 别顺手在 `opportunities` 上加 `last_contact_note` 这类字段——那是另一个组件的聚合根 |
| 线索 | `crm-lead`（阶段六） | 线索转商机是那时的事 |

`data_scopes` 声明 `org`（`dept_path` 前缀）+ `owner`（`owner_id` 相等）两维，落在 `opportunities`。⚠️ **与 `erp-sales` 完全一致，这是刻意的**：赢单转订单时订单要继承商机的归属，两边维度对不上就会出现"商机我看得见、转出来的订单我看不见"。`dept_id`/`dept_path`/`owner_id` 是**创建时快照**，不是运行时查"这个人现在在哪个部门"。

## 契约面与事件

**gRPC `crm.opportunity.v1.OpportunityService`：** `CreateOpportunity`（建档，校验客户与产品）、`UpdateOpportunity`（改金额/预计成交日/负责人，全量替换+乐观锁）、`ChangeStage`（阶段推进，写历史，任意跳转）、`MarkWon`（⭐ 链路的扳机）、`MarkLost`（要求填原因）、`GetOpportunity`/`BatchGetOpportunities`（防 N+1）、`ListOpportunities`（走 ScopeFilter）、`ListStages`（阶段是数据不是代码，供前端读）。

**REST 前缀：** `/crm/opportunity/**`。`BatchGetOpportunities` 不暴露（组件间协议）。赢单单独一个权限键 `crm.opportunity.win`，不与普通编辑合并——它会触发建单、锁库存、生成应收一整条链路，是本组件唯一有下游财务影响的动作。

**发布事件：** ⭐ `crm.opportunity.won.v1`（核心）——`MarkWon` 成功提交后经 Outbox 发。**owner_id/dept_path 两个字段最容易漏**：`erp-sales` 的 handler 跑在事件消费路径（`SystemClient`/无 JWT 上下文），漏了这两个字段订单会建出来但没有归属，"我的订单"/组织树前缀匹配都查不到它，且不报任何错。`items[].quoted_unit_price` 是报价快照供参考，`erp-sales` 会用自己的定价引擎重算，不是权威价目。`crm.opportunity.lost.v1`（旁路，供未来 BI）、`.stage_changed.v1`（旁路，供漏斗转化率分析）。

⚠️ **subject 是三段式 `crm.opportunity.won.v1`，不是两段式**——命名法是 `{domain}.{aggregate}.{action}`。这是唯一还能改名的窗口，`erp-sales` 一旦开始消费就不能再改（决策 19：只增不删不改）。

**消费事件：** `mdm.customer.created.v1`/`.updated.v1`（维护 `customer_snapshots.name`，按 `version` 单调比较，旧的丢弃）。⚠️ **不消费 `erp-sales` 的任何事件**——赢单之后订单建成没有、发货没有，一概不追踪，那会让 CRM 反向依赖 ERP 的状态机。想在商机上看到"已转订单"，正确做法是前端/BFF 分别查两边再聚合，不是本组件去持有订单状态。

## 依赖与「为什么不依赖某某」

两条强依赖：`mdm/customer`（`BatchGet` 校验客户存在+取展示快照）、`mdm/product`（`BatchGet` 校验产品存在+取展示快照）。均用 `besdk.UserClient`（透传 JWT），**不许用 `SystemClient`**——用户请求路径，`SystemClient` 会绕过下游数据权限，`make gates` 有扫描守着。

**明确不依赖：**

| 谁 | 为什么不建依赖边 |
|---|---|
| ⭐ `erp-sales` | **§1.4 铁律：CRM 与 ERP 零同步边，仅事件握手。** 这是本组件最重要的一条禁令——附录 E"商机赢单→建订单"链路看起来就该建一条同步边，很容易顺手写成一次同步调用。正确形态是事件握手：我把"赢了"这个事实广播出去，谁关心谁消费；订单建不建得成，与商机赢没赢是两件独立的事 |
| `erp-inventory` / `erp-finance` | 同上，而且更远——商机阶段根本不该知道库存和应收的存在 |
| `infra-workflow` | 本阶段商机不走审批。⚠️ 将来"大额商机赢单要审批"是业务规则，那时也是本组件调 workflow，不是反过来 |
| `infra-iam-casdoor` | JWT 本地验签，`iamJwksUrl` 配置项 |

**同步图位置：** 本组件在同步图上有两条出边（`mdm/customer`、`mdm/product`），零入边。无环证明：两个 `mdm` 都是只读枢纽、零出边，所以从本组件出发的路径长度恒为 1。

## 这个组件特有的坑

| 不许 | 症状 | 出处 |
|---|---|---|
| 给 `crm-opportunity → erp-sales`（或任何 ERP 组件）建同步依赖边 | 两个域的事务耦合——赢单这个动作会因为库存不足/应收失败而失败，而销售明明已经赢了；CRM 装配上就绑死 ERP，只买 CRM 不买 ERP 的客户装不了；同步图跨域，§1.4 的整条边界失效 | 设计计划 §6 |
| `crm.opportunity.won.v1` 的 payload 漏带 `owner_id`/`dept_path` | `erp-sales` 的事件 handler 跑在系统身份下没有 JWT，只能靠事件带归属——漏带的后果是订单没有归属人/组织前缀匹配匹配不上，且不报任何错，订单建出来了、库存也占了、应收也生成了，只是列表里查不到 | 设计计划 §4.1 |
| 把商机行的 `quoted_unit_price` 当权威价格用（比如拿它去校验 `erp-sales` 建出来的订单价格是否"正确"） | 这是报价快照，`erp-sales` 会用自己的定价引擎重算，两者允许不一致——这不是 bug，是刻意的设计（定价规则归 `erp-sales`） | 设计计划 §2.1 |
| 给 `opportunities`/`opportunity_items`/`opportunity_stage_history` 加分区 | 这三张表明确不分区（设计计划 §2、§7，跟随 §11.2.5 分区大表清单——那张表里没有本组件）：商机是每个销售机会一行，量级比交易流水低一到两个数量级 | 设计计划 §2、§7 |
| 让 `expected_amount × probability` 的加权金额落库存字段 | 改了金额或概率后历史行的加权值会漂移——必须是读时现算的派生值，不是存储字段 | 设计计划 §2 |
| 实现"必须按顺序推进阶段""跳阶段要审批"这类约束 | 这是刻意判定的 Fork 点（设计计划 §8 的 R-4 判定），不是遗漏——本阶段任意阶段之间都可以自由跳转，只记历史。客户需要更严格流程管控时走定制目录，不要在标准版里加 `if` 分支 | 设计计划 §2.2、§8 |
| 给 `event_outbox`/`event_inbox` 建仓库时不接周分区维护循环 | 第 5 周起 `INSERT` 会因为找不到覆盖的分区直接失败，报错是 PostgreSQL 原生的"no partition of relation found for row"，完全看不出根因——`infra-workflow` 真实踩过这个坑（根 `docs/dev/实测踩坑记录.md` C12），本组件从第一次提交起就直接接好，不要"先建表后面再补" | 根 `docs/dev/实测踩坑记录.md` C12 |
| 给本组件加月分区维护循环 | 本组件没有任何月分区表——`opportunities` 系列三张表不分区，只有 `event_outbox`/`event_inbox` 需要周分区维护，加一个从不生效的月分区循环是死代码 | 设计计划 §2、§7 |
| 测 `MarkWon` 真的发布到 NATS 时假设"收到的第一条消息就是我要的那条" | `crm.opportunity.won.v1` 是全部商机共用的 subject，Postgres 里可能还躺着其它测试留下的旧 `PENDING` 行（例如验证"已终态不能重复赢单"的测试，它调用一次成功的 `MarkWon` 但从不跑推送循环去清空）——这是实现测试时真实撞到过的坑，正确做法是按 `opportunity_id` 过滤，不是假设消息到达顺序 | `backend/internal/repo/write_test.go` |

## 改代码前的自查

1. **我是不是在给 `crm-opportunity` 加一条指向 `erp-*`/`infra-workflow` 的依赖边？** 停下——§1.4 铁律，CRM 与 ERP 零同步边，仅事件握手，改用事件通知。
2. **我是不是漏带 `crm.opportunity.won.v1` 的 `owner_id`/`dept_path`？** 停下——这是消费方（`erp-sales`）唯一能拿到归属信息的地方，漏了不报错但订单查不到。
3. **我是不是想把商机行的报价当权威价格去校验/对账？** 停下——那是报价快照，`erp-sales` 会重算，不一致是允许的。
4. **我是不是在给 `opportunities`/`opportunity_items`/`opportunity_stage_history` 三张表之一加分区？** 停下——设计计划明确它们不分区，量级不到需要分区的程度。
5. **我是不是在实现阶段推进的顺序/审批约束？** 停下——这是设计计划 §8 判定的 Fork 点，标准版不做，客户需要时走定制目录。
6. **我调 `mdm-customer`/`mdm-product` 用的是 `UserClient` 还是 `SystemClient`？** 用户请求路径上只许 `UserClient`。
7. **我是不是在给一张新的分区表建仓库却忘了接周分区维护循环？** 停下——同 C12 的教训，从第一次提交起就要接好，不要"先建表后面再补"。
8. **这个改动会不会让 `contracts/crm/opportunity/v1/opportunity.proto` 出现破坏性变更？** 下游（`erp-sales` 未来的消费者实现、BFF）都会消费这份契约，只能向后兼容地追加。
