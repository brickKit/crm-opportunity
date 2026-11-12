# masterdata：事务外取证（共用）

向主数据组件取证，都在事务之外（线协议 P4.20）；不可达不等于不存在（协议 07）：答“依赖不可用”。

## 负责与不负责

- 负责：法人（`mdm/org`：存在、活跃、代码、本位币、时区）、币种（`mdm/currency`：存在、活跃、小数位）、客户（`mdm/partner`：用途 `sell` 的资格与名称的快照）。
- 不负责：怎样用这些证据（各模块）。

## 文件

| 文件 | 内容 |
| --- | --- |
| `upstream.go` | `LegalEntityOf`、`CurrencyScale`（不可用答 `REFERENCE_INVALID`）、`CustomerOf`（不合格答 `CUSTOMER_NOT_ELIGIBLE`，带伙伴的原因） |

## 依赖与被依赖

- 依赖：`core`；契约 `mdm-org`、`mdm-currency`、`mdm-partner`。
- 被依赖：`opportunities`（新建与修改）、`forecast`（加权金额的舍入）。

## 规则在哪

设计 OP01、OP02；装配项目协议 07 的资格引用。

## 测试

`itest/opportunities_integration_test.go`（替身答法人、币种与资格的各种情形）。
