# opportunities：商机与它的生命周期

商机、它的阶段与阶段历史（设计 OP01–OP04、OP06、OP07），以及每次改商机都经过的入口 `ChangeIn`。

## 负责与不负责

- 负责：表 `opportunity`、`opportunity_line`、`stage_change`；新建（取证、单号、第一行阶段历史）；修改（赢单后锁金额）；推进与回退；改负责人；赢单、输单、重开（关闭前的阶段与概率）；事件 `crm.opportunity.changed.v1`、`stage_changed`、`won`、`lost`；读取（带阶段历史、请求报价、关联的销售单据）与列表。
- 不负责：请求报价与销售单据回来（`quotes`，它经 `ChangeIn` 与 `Win` 改商机）；漏斗（`forecast`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `lifecycle.go` | 商机的表（`core.Table`）：状态 `open`、`won`、`lost`，九个命令；`PhaseOf`；与设计的表逐格相同 |
| `lifecycle_test.go` | 单元测试：表的每格完整、与设计逐格相同、每条规则有集成测试提到 |
| `records.go` | `Opportunity`（完整状态）、`Read`（加历史、请求、销售单据）、`Cols`、`Scan`、`Get`、`Load`、`ReadOf` |
| `commands.go` | 校验（阶段、概率、明细 `CheckLines`、名称）；`Create`（OP01）、`WriteLines`、`moved`（阶段历史与事实）、`answer`、`GetOpportunity` |
| `changes.go` | `Run`（用户的命令：授权、按格）、`ChangeIn`（事务里：锁行、按格、版本、保存、事件、审计）、`save`；`Edit`（OP02）、`Move`（OP03）、`Assign`（OP04）、`Close`（OP06、OP07）、`Win`（也给自动赢单用）、`lose`、`reopen` |
| `list.go` | 列表（游标、筛选、搜索单号与名称；数据范围由 SDK 编进查询） |

## 依赖与被依赖

- 依赖：`core`、`masterdata`。
- 被依赖：`quotes`、入口（路由）。

## 表

`opportunity`、`opportunity_line`、`stage_change`。

## 规则在哪

设计 OP01–OP04、OP06、OP07 与“商机”一表。

## 测试

`lifecycle_test.go`；`itest/opportunities_integration_test.go`（`opportunities`）。
