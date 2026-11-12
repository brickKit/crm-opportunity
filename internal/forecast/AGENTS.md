# forecast：漏斗与预测方法

漏斗查询与预测方法；预测方法是设计写的替换点：Fork 换掉 `weighted`，查询的形状不变。

## 负责与不负责

- 负责：路由 `GET /api/opportunity/funnel`：看得见的商机按法人、币种、阶段的个数、金额（赢单用赢单金额）、加权金额（按币种的小数位舍入）。
- 不负责：商机的数据（`opportunities`）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `funnel.go` | `value`、`weighted`（本版：按阶段概率加权）、`Funnel`、`round` |

## 依赖与被依赖

- 依赖：`core`、`masterdata`（币种的小数位）。
- 被依赖：入口（路由）。

## 表

读 `opportunity`。

## 规则在哪

设计的“命令与查询”（漏斗）与“部署与替换”。

## 测试

`itest/funnel_integration_test.go`（`funnel`）。
