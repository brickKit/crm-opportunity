-- Outbox / Inbox。所有组件都有这两张表，且都按 created_at 周分区（§11.2.5）
-- 保留周期：已发布成功超过 30 天可清理（§11.7、决策 62）
--
-- ⚠️ 初始分区覆盖当前周起 4 周（迁移执行时是 2026-09-07 那一周）。
-- 其余分区由组件内置定时任务自动建（决策 54、§11.5.1）——这里只需要
-- 保证迁移跑完那一刻起系统能正常写入，不需要预先建满未来所有分区。
--
-- ⚠️ 这两张表从建仓库起就必须带周分区维护循环（backend/internal/
-- partition），不能"先建表、后面再补循环"——infra-workflow 就是漏了
-- 这个循环，第 5 周起 INSERT 会因为找不到覆盖的分区直接失败，且报错是
-- PostgreSQL 原生的"no partition of relation found for row"，完全看不出
-- 根因（根 docs/dev/实测踩坑记录.md C12）。

CREATE TABLE event_outbox (
    id           BIGSERIAL,
    subject      TEXT        NOT NULL,
    aggregate_id TEXT        NOT NULL,
    version      BIGINT      NOT NULL,
    trace_id     TEXT        NOT NULL DEFAULT '',
    causation_id TEXT        NOT NULL DEFAULT '',
    hop_count    INT         NOT NULL DEFAULT 0,
    payload      JSONB       NOT NULL,
    published_at TIMESTAMPTZ,
    attempts     INT         NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    status       TEXT        NOT NULL DEFAULT 'PENDING',
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);
CREATE TABLE event_outbox_2026_09_07 PARTITION OF event_outbox
  FOR VALUES FROM ('2026-09-07') TO ('2026-09-14');
CREATE TABLE event_outbox_2026_09_14 PARTITION OF event_outbox
  FOR VALUES FROM ('2026-09-14') TO ('2026-09-21');
CREATE TABLE event_outbox_2026_09_21 PARTITION OF event_outbox
  FOR VALUES FROM ('2026-09-21') TO ('2026-09-28');
CREATE TABLE event_outbox_2026_09_28 PARTITION OF event_outbox
  FOR VALUES FROM ('2026-09-28') TO ('2026-10-05');
-- 其余分区由定时任务自动建（决策 54）
CREATE INDEX event_outbox_pending ON event_outbox (status, created_at)
  WHERE status = 'PENDING';

-- ⚠️ 实测踩坑（同其它组件的 002 迁移）：建分区要求执行者是父表的
-- owner，迁移用管理凭据跑，建出来的表默认属于那个账号；分区维护后台
-- 任务运行时用 crm_opportunity_rw（SET LOCAL ROLE 切换）建未来的分区，
-- 两者不是同一身份，必须显式把 owner 转过去。
ALTER TABLE event_outbox OWNER TO crm_opportunity_rw;

CREATE TABLE event_inbox (
    id              BIGSERIAL,
    idempotency_key TEXT        NOT NULL,
    subject         TEXT        NOT NULL,
    aggregate_id    TEXT        NOT NULL,
    version         BIGINT      NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    status          TEXT        NOT NULL DEFAULT 'PROCESSED',
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);
CREATE TABLE event_inbox_2026_09_07 PARTITION OF event_inbox
  FOR VALUES FROM ('2026-09-07') TO ('2026-09-14');
CREATE TABLE event_inbox_2026_09_14 PARTITION OF event_inbox
  FOR VALUES FROM ('2026-09-14') TO ('2026-09-21');
CREATE TABLE event_inbox_2026_09_21 PARTITION OF event_inbox
  FOR VALUES FROM ('2026-09-21') TO ('2026-09-28');
CREATE TABLE event_inbox_2026_09_28 PARTITION OF event_inbox
  FOR VALUES FROM ('2026-09-28') TO ('2026-10-05');
-- 消费幂等靠这个唯一约束（§4.6：被调用方在数据库中使用唯一约束去重）。
CREATE UNIQUE INDEX event_inbox_idem ON event_inbox (idempotency_key, created_at);

-- 同上：分区维护后台任务要能给 event_inbox 建未来的分区。
ALTER TABLE event_inbox OWNER TO crm_opportunity_rw;

-- 写命令的幂等表（命令侧，与事件消费侧的 event_inbox 分开）。
-- CreateOpportunity/UpdateOpportunity/ChangeStage/MarkWon/MarkLost 的
-- idempotency_key 落在这里去重；result_id 存该命令产生/影响的商机 id。
CREATE TABLE command_idempotency (
    idempotency_key TEXT        PRIMARY KEY,
    command         TEXT        NOT NULL,
    result_id       TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
