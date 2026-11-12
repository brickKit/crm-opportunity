# itest/：集成测试与测试索引

商机组件对着真实的 PostgreSQL 与消息系统、经它真实的接口（用户面 HTTP、事件）的测试（L2）。构建标签 `integration`；一条业务流程一个文件。

## 怎样跑

- `make test-integration`（`go test -count=1 -tags integration ./itest/`），几秒。
- 环境变量：`BE_SDK_TEST_PG_ADMIN`（口令是装配项目 `.env` 的 `POSTGRES_PASSWORD`）、`BE_SDK_TEST_NATS_URL=nats://localhost:4222`；be-sdk-go 的 `sectest` 是测试部署。
- 前提：装配项目里 `make up` 起了 `be-postgres` 与 `be-nats`。
- 端口：28102（用户面）、9102（系统面）、替身 19150（`mdm/org`）、19151（`mdm/partner`）、19152（`mdm/currency`）要空着，被占时测试直接失败。
- 每次开始：删掉再建自己的 schema `crm_opportunity_it`（跑完留着，可以查）；删掉流 `erp.sales`、`crm.opportunity`。
- 自动赢单设成 `order_confirmed`（另两种设置由 `internal/quotes` 的单元测试覆盖）。
- **只有一个测试** `TestOpportunityEndToEnd`：它按下表的顺序跑各步；改了哪个流程就跑整个。

## 文件

按运行顺序：

| 文件 | 步 | 测的流程 | 覆盖的规则 | 被测的模块 |
| --- | --- | --- | --- | --- |
| `opportunities_integration_test.go` | `opportunities` | 新建（本位币、创建人为负责人、缺省阶段与概率、单号；同一幂等键；北京没开客户角色、暂停的客户、不认识的法人与币种、小数位、终态阶段、空名称被拒）；指定别人为负责人：看不见的答 404，Erin 能编辑全部但没有改负责人的操作答 403，Bob 可以；按版本修改；推进跨步、不能推到同一或更早的阶段、不能推到终态、回退要原因、给定概率；Dave 只看到自己的、不能改负责人，Bob 改负责人后 Dave 看不到；赢单（金额、再赢单不变、只一个事实、金额锁定、名称可改、赢单后不能推进与输单）；重开回到关闭前的阶段与概率、重开进行中的忽略；输单要原因、再输单忽略、输单后不能赢单与修改；再重开再赢单发新的事实 | OP01–OP04、OP06、OP07 | `opportunities`、`masterdata` |
| `quotes_integration_test.go` | `quotes` | 请求报价的事实（单号、币种、明细）、同一键答同一个请求、给明细就替换商机的；赢单的不能请求；测试扮演销售：草稿答复第一个请求、拒绝第二个；报价被接受与订单草稿只记下；订单确认按不含税合计自动赢单（操作者 `system`、触发是订单）；旧版本不覆盖、同一确认订单再来不再赢单；美元订单按预计金额赢单并写明原因；输单的只记下；手工报价与不认识的商机丢弃 | OP05、OP08 | `quotes`、`opportunities` |
| `funnel_integration_test.go` | `funnel` | 北京的商机按阶段：个数、金额、加权按币种舍入；赢单按赢单金额、输单加权为 0；Dave 只算自己的；按负责人与预计成交日筛选；筛选值形式不对答 400 | 漏斗 | `forecast` |
| `outbox_integration_test.go` | `outbox` | 每个事件合乎 schema，每种类型都发过 | 协议 02 | 全部发事件的模块 |
| `recovery_integration_test.go` | `recoveryChecks` | 作为恢复工具：别的组件被拒；审计过的商机都在、高一千版答“旧了”、新身份答“不在” | be-protocol 第 14 章 | 入口 `opportunity/checks.go` |

共用的：

| 文件 | 内容 |
| --- | --- |
| `endtoend_integration_test.go` | `TestOpportunityEndToEnd`（各步的顺序）；`fixture`（运行中的成员、替身、世界的 ID：上海与北京两个法人、一个客户、只在上海开了客户角色的客户、暂停的客户；Alice 全部、Bob 是销售主管、Dave 是只管自己商机的销售、Erin 能编辑全部但不改负责人）；`start`；辅助：`own`（只管自己的授予）、`everyone`（全部负责人的授予）、`outboxed`、`client.do`、`at`、`get`、`publishSales`（扮演销售的转发器发事件）、`portFree` |
| `standins_integration_test.go` | 替身，各按已发布的契约经 mTLS 答：`mdm/org`（法人）、`mdm/partner`（资格：暂停、某法人没开客户角色）、`mdm/currency`（CNY 与 USD） |

## 加测试

- 新流程：新文件 `<流程>_integration_test.go`，写一个 `func (fx *fixture) <步>()`，在 `TestOpportunityEndToEnd` 里按依赖排进顺序，在上表加一行；测生命周期规则的步在注释里写明编号（`TestEveryRuleIsTested` 核对）。
- 测试不为了通过而改（装配项目规范的铁律）。
