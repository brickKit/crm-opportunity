[English](README.md) · [中文](README.zh.md)

# crm/opportunity

商机生命周期、销售漏斗与赢单预测数据；发布赢单、输单与阶段变更事件。

## 在项目里使用

```bash
brickkit add crm/opportunity@2.0.0
brickkit up
```

`mdm/customer` 与 `mdm/product` 作为依赖一起加入。先准备好：见 BRICKKIT.zh.md 的"部署前准备"（PostgreSQL 的 schema 与登录角色、NATS、权限与 JWKS 的地址）。要让赢单商机变成订单，项目里再加上 `erp/sales`；没有它本组件照样能用。

## 文档

| 想知道 | 读 |
|---|---|
| 它做什么、不做什么，怎么配置，要准备什么 | [BRICKKIT.zh.md](BRICKKIT.zh.md) |
| 它的 gRPC、REST 与事件契约 | [contracts/](contracts/) |
| 为什么这样设计：与 ERP 的边界、事件、数据范围、未决问题 | [docs/design.zh.md](docs/design.zh.md) |
| 它依赖什么、有哪些配置键 | [component.yaml](component.yaml) |
| 怎么开发：代码地图、测试、易错点 | [AGENTS.zh.md](AGENTS.zh.md) |

## 开发

Go 1.25、Gin、`database/sql` 加 pgx、经 be-sdk-go 用 golang-migrate。测试需要真实的 PostgreSQL（`TEST_PG_DSN`）与 NATS；命令和成功的样子见 AGENTS.zh.md 的"构建与测试"。在 BrickEnterprise 项目里，项目根 `make verify ID=crm/opportunity FOCUS=1` 真实地跑一遍（容器形态与本地进程），跑完拆掉。
