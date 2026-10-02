-- 阶段流转历史记下这次流转设定的概率（阶段默认值或手工覆盖值），供
-- GET /opportunities/{id}/stage-history 返回。加列之前写下的历史行没有这个
-- 信息，保持 NULL，不按阶段默认值去猜。
ALTER TABLE opportunity_stage_history ADD COLUMN IF NOT EXISTS probability INT;
