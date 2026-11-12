# crm/opportunity

## 组件定位

商机：一笔可能成交的生意，从发现到赢单或输单。
- **商机**：法人、客户（业务伙伴的客户角色，新建时取资格）、预计金额与成交日、负责人、产品明细（给报价预填）。
- **阶段**：一条固定的管道（资格确认、需求分析、方案报价、谈判，终态赢单、输单），向前可跨步，向后要写原因；阶段历史；每个阶段有缺省概率。
- **赢单、输单、重开**：手动；或按部署的设置，由销售的报价被接受、订单被确认自动赢单。赢单后金额锁定，改要重开。
- **请求报价**：发布事实，`erp/sales` 生成带商机参考的报价草稿；销售的报价、订单与拒绝请求的事实回到商机。
- **漏斗**：按法人、币种、阶段的个数、金额与按概率加权的金额（预测方法是替换点）。

CRM 与 ERP 只经事件协作：本组件与 `erp/sales` 互不调用、互不声明依赖。本版没有：可配置的管道、部门维度、`crm/customer` 的负责人缺省、按币种折算的漏斗、扩展字段。

**3.0.0**（第一个重建的版本，契约 `contract/v3.0.0-alpha.2`；3.0.1 起 alpha.3）：商机与阶段、赢单输单与重开、请求报价与销售单据回来（含自动赢单）、漏斗、整套恢复的核对。

## 部署前准备

- 数据库：PostgreSQL 16 及以上；schema `crm_opportunity`，属主角色 `crm_opportunity_owner`（只供迁移）与运行角色 `crm_opportunity_rw`（只有读写数据的权限），名字来自装配项目的注册表。三者由部署方在第一次启动前建好；表由本组件的迁移建，迁移里不建角色、不授权。
- 消息系统（`BUS_URL`）与实现实例（`IMPLEMENTATION_INSTANCE`）：商机的完整状态与四种增量事实发布到流 `crm.opportunity`（4 个分区）；订阅 `erp/sales` 的流 `erp.sales`（报价、订单的完整状态，拒绝请求报价的事实）。没装 `erp/sales` 时那个流是空的，订阅只是等着。
- 装了 `erp/sales` 时它订阅本组件的 `crm.opportunity.quote_requested.v1`（`erp/sales` 3.0.50 起），两边都不声明对方为依赖。
- 部署的服务证书（`SERVICE_TLS_*`）；身份与授权（`IAM_*`、`AUTHZ_GRPC_URL`、`DEPLOYMENT_ID`）；审计（`AUDIT_GRPC_URL`）。
- 自动赢单的设置 `OPPORTUNITY_AUTO_WIN` 是客户的业务选择，装配时问清（缺省 `manual`）。
- 低负载：适合外壳。

## 依赖说明

- 必需：数据库、消息系统、身份成员、授权成员、`mdm/partner`（客户资格）、`mdm/org`（法人的证据）、`mdm/currency`（币种与小数位）。
- 只经事件：`erp/sales`（不声明；缺时请求报价照样发布，请求停在 `requested`，赢单与输单手工照常）。
- 可选：审计组件。
- 主数据不可达时：新建、改客户或币种、指定赢单金额答“依赖不可用”、什么也不改，可以重试；已有数据照常读取。

## 配置指南

全部键见 `component.yaml` 的 `configSchema`（由 SDK 生成，测试核对）。

| 键 | 说明 |
| --- | --- |
| `OPPORTUNITY_AUTO_WIN` | 销售单据何时让它的商机赢单：`manual`（从不，缺省）、`quotation_accepted`（报价被接受）、`order_confirmed`（订单被确认）；赢单金额是单据的不含税合计，币种不同时用商机的预计金额 |
| `DEPLOYMENT_ID`、`IAM_ISSUER`、`IAM_JWKS_URL`、`AUTHZ_GRPC_URL`、`SERVICE_TLS_CERT_FILE`、`SERVICE_TLS_KEY_FILE`、`SERVICE_TLS_CA_FILE`（必填） | 受保护的用户面与 mTLS 的系统面要用；部署身份、身份与授权的地址在外壳里是进程级的键 |
| `PG_HOST`、`PG_DATABASE`、`PG_USER`、`PG_PASSWORD`、`PG_SCHEMA`（必填） | 自己的数据库，密码是密钥；迁移另用 `PG_OWNER_USER`、`PG_OWNER_PASSWORD` |
| `BUS_URL`（必填，密钥）、`IMPLEMENTATION_INSTANCE`（必填） | 消息系统；实现实例是安装时生成的随机身份 |
| `AUDIT_GRPC_URL` | 审计组件的系统面；缺时审计记录留在本地，按协议 03 处理 |

依赖的地址由 BrickKit 按依赖注入（`MDM_ORG_GRPC_ENDPOINT`、`MDM_PARTNER_GRPC_ENDPOINT`、`MDM_CURRENCY_GRPC_ENDPOINT`），不是配置键。单号格式 `OP-<法人代码>-<年>-<六位序号>`（允许缺号）。

## 契约索引

- **系统面**：没有本组件自己的服务（补发、整套恢复的核对是 SDK 的标准服务）。
- **用户面**：`contract/openapi.yaml`。操作 `crm.opportunity.read`（列表、读取、漏斗）、`.edit`（新建、修改、推进、回退、请求报价）、`.close`（赢单、输单、重开）、`.assign`（改负责人；新建时指定别人为负责人也要它）；维度：法人、负责人（“销售只看自己的商机”）。新建与请求报价要带 `Idempotency-Key`。
- **事件**（流 `crm.opportunity`，schema 在 `contract/events/`）：`crm.opportunity.changed.v1`（完整状态）；`crm.opportunity.stage_changed.v1`、`crm.opportunity.quote_requested.v1`（`erp/sales` 按事实身份生成一张报价草稿）、`crm.opportunity.won.v1`、`crm.opportunity.lost.v1`（增量事实）。

## 外壳声明

不是外壳。可以被 Go 的核心外壳（`be/go-core`）承载。
