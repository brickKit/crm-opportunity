-- crm-opportunity 核心表：商机主体/行/阶段流转历史/阶段定义、客户展示
-- 快照。schema 由迁移工具的 search_path 指定，SQL 里不写限定名。
-- ⚠️ 迁移状态表必须落在本组件 schema 里（§11.2.3）：golang-migrate 的
-- x-migrations-table + search_path，见 backend/cmd/migrate/main.go

-- 阶段定义：数据不是代码（设计计划 §2.2），迁移播种，见 003_seed_stages。
-- 不分区（配置型主数据，量级极小）。
CREATE TABLE opportunity_stages (
    id                   BIGSERIAL PRIMARY KEY,
    code                 TEXT           NOT NULL UNIQUE,
    name                 TEXT           NOT NULL,
    sort_order           INT            NOT NULL,
    default_probability  INT            NOT NULL DEFAULT 0,
    is_final             BOOLEAN        NOT NULL DEFAULT false,
    -- §11.2.1 强制字段
    created_at           TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ    NOT NULL DEFAULT now(),
    version              BIGINT         NOT NULL DEFAULT 1,
    status               TEXT           NOT NULL DEFAULT 'ACTIVE',
    CONSTRAINT opportunity_stages_probability_range CHECK (default_probability BETWEEN 0 AND 100)
);
CREATE INDEX opportunity_stages_sort ON opportunity_stages (sort_order);
ALTER TABLE opportunity_stages OWNER TO crm_opportunity_rw;

-- 商机主体。⚠️ 不分区（设计计划 §2、§7）——跟随 §11.2.5 分区大表清单
-- （那张表里有 crm-activity，没有 crm-opportunity）：活动记录是每次
-- 通话/拜访一行，商机是每个销售机会一行，量级差一到两个数量级。
--
-- ⚠️ 加权金额（expected_amount × probability）算出来不存，落库会漂移
-- （设计计划 §2）——由读接口在 backend/internal/repo 现算。
--
-- org（dept_path 前缀）+ owner（owner_id 相等）两维数据权限，与
-- erp-sales 完全一致（assembly.yaml 已声明）：赢单转订单时订单要继承
-- 商机的归属，两边维度对不上就会出现"商机我看得见、转出来的订单我
-- 看不见"（设计计划 §1）。dept_id/dept_path/owner_id 是**创建时快照**，
-- 不是运行时查"这个人现在在哪个部门"（§11.2.1 明文，同 sales_orders
-- 的既有判据）。
CREATE TABLE opportunities (
    id                   BIGSERIAL PRIMARY KEY,
    name                 TEXT           NOT NULL,
    customer_id          TEXT           NOT NULL,             -- 不透明外键，来自 mdm-customer
    customer_name        TEXT           NOT NULL DEFAULT '',  -- 快照
    owner_id             TEXT           NOT NULL DEFAULT '',
    dept_id              TEXT           NOT NULL DEFAULT '',
    dept_path            TEXT           NOT NULL DEFAULT '',
    stage_id             BIGINT         NOT NULL REFERENCES opportunity_stages (id),
    expected_amount      NUMERIC(18,2)  NOT NULL DEFAULT 0,
    probability          INT            NOT NULL DEFAULT 0,   -- 阶段带默认值 + 允许手工覆盖（设计计划 §9 第 4 条）
    expected_close_date  DATE,
    status               TEXT           NOT NULL DEFAULT 'OPEN',
    currency             TEXT           NOT NULL DEFAULT 'CNY',
    lost_reason          TEXT           NOT NULL DEFAULT '',
    won_at               TIMESTAMPTZ,
    -- §11.2.1 强制字段（status 复用为业务状态，同 sales_orders 的既有判据）
    created_at           TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ    NOT NULL DEFAULT now(),
    version              BIGINT         NOT NULL DEFAULT 1,
    CONSTRAINT opportunities_status_valid CHECK (status IN ('OPEN', 'WON', 'LOST', 'CANCELLED')),
    CONSTRAINT opportunities_probability_range CHECK (probability BETWEEN 0 AND 100)
);
CREATE INDEX opportunities_customer ON opportunities (customer_id);
CREATE INDEX opportunities_stage    ON opportunities (stage_id);
-- org/owner 数据权限维（设计书 §14.2.2、assembly.yaml 已声明）
CREATE INDEX opportunities_dept_path ON opportunities (dept_path text_pattern_ops);
CREATE INDEX opportunities_owner     ON opportunities (owner_id);
ALTER TABLE opportunities OWNER TO crm_opportunity_rw;

-- 商机行：这次机会打算卖哪些产品、多少量、什么价。全部快照，不 join
-- （设计计划 §2.1，同 sales_order_items 的既有判据）：产品数据在另一个
-- 进程另一个 schema 里，物理上 join 不到。⚠️ quoted_unit_price 是报价
-- 快照供参考，不是权威价目——erp-sales 赢单转订单时会用自己的定价
-- 引擎重算，两者允许不一致。
--
-- 不分区（跟随主表），普通 FK 直接指向 opportunities(id)——不像
-- sales_order_items 需要复合 FK 应对分区键，因为 opportunities 本身
-- 就不分区。
CREATE TABLE opportunity_items (
    id                 BIGSERIAL      PRIMARY KEY,
    opportunity_id     BIGINT         NOT NULL REFERENCES opportunities (id),
    product_id         TEXT           NOT NULL,              -- 不透明外键，来自 mdm-product
    product_sku        TEXT           NOT NULL DEFAULT '',
    product_name       TEXT           NOT NULL DEFAULT '',
    uom_id             TEXT           NOT NULL DEFAULT '',
    qty                NUMERIC(18,6)  NOT NULL,
    quoted_unit_price  NUMERIC(18,2)  NOT NULL DEFAULT 0,
    subtotal           NUMERIC(18,2)  NOT NULL DEFAULT 0,
    -- §11.2.1 强制字段
    created_at         TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ    NOT NULL DEFAULT now(),
    version            BIGINT         NOT NULL DEFAULT 1,
    status             TEXT           NOT NULL DEFAULT 'ACTIVE',
    CONSTRAINT opportunity_items_qty_positive CHECK (qty > 0)
);
CREATE INDEX opportunity_items_opportunity ON opportunity_items (opportunity_id);
CREATE INDEX opportunity_items_product     ON opportunity_items (product_id);
ALTER TABLE opportunity_items OWNER TO crm_opportunity_rw;

-- 阶段流转历史：只增不改（设计计划 §2）。同 sales_order_reconciliation_
-- queue 的既有判据——纯追加日志不需要 §11.2.1 那套 created_at/updated_at/
-- version/status 四件套，只需要一个时间戳。from_stage_id 允许为空
-- （建档时的初始赋值，不算"流转"，见 backend 侧只在真正 ChangeStage
-- 时才写这张表）。
CREATE TABLE opportunity_stage_history (
    id              BIGSERIAL   PRIMARY KEY,
    opportunity_id  BIGINT      NOT NULL REFERENCES opportunities (id),
    from_stage_id   BIGINT      REFERENCES opportunity_stages (id),
    to_stage_id     BIGINT      NOT NULL REFERENCES opportunity_stages (id),
    changed_by      TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX opportunity_stage_history_opportunity ON opportunity_stage_history (opportunity_id, created_at);
ALTER TABLE opportunity_stage_history OWNER TO crm_opportunity_rw;

-- mdm-customer 的展示快照：只取 name（设计计划 §5 的三方分工——本组件
-- 不关心信用额度，那是 erp-sales 的事）。
CREATE TABLE customer_snapshots (
    customer_id TEXT           PRIMARY KEY,
    name        TEXT           NOT NULL DEFAULT '',
    -- §11.2.1 强制字段
    created_at  TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ    NOT NULL DEFAULT now(),
    version     BIGINT         NOT NULL DEFAULT 1,
    status      TEXT           NOT NULL DEFAULT 'ACTIVE'
);
ALTER TABLE customer_snapshots OWNER TO crm_opportunity_rw;
