-- 种阶段清单（设计计划 §2.2：阶段是数据不是代码）。名称与默认概率照抄
-- 设计计划 §2 给出的例子，✅ 已查证 Odoo 的 probability 语义（阶段带
-- 默认值 + 允许手工覆盖，设计计划 §8/§9 第 4 条）。
--
-- ⚠️ is_final 只标最后一个"赢单"阶段——本阶段不学 Odoo 的
-- probability=100 自动关闭（那样一个数字改动就会触发建单，见设计计划
-- §8）；ChangeStage 到这个阶段本身不会转 WON，必须显式调用 MarkWon
-- 才会触发赢单事件链路，两者是独立动作。
INSERT INTO opportunity_stages (code, name, sort_order, default_probability, is_final) VALUES
    ('INITIAL_CONTACT', '初步接洽', 10, 10, false),
    ('NEEDS_CONFIRMED', '需求确认', 20, 30, false),
    ('PROPOSAL',        '方案报价', 30, 50, false),
    ('NEGOTIATION',     '商务谈判', 40, 70, false),
    ('WON',             '赢单',     50, 100, true);
