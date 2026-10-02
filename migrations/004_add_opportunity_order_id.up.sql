-- 赢单转出的订单 id：由消费 erp/sales 的 sales.order.created.v1 回填，还没有
-- 订单时为空串。只存 id，订单的状态归 erp/sales。
ALTER TABLE opportunities ADD COLUMN IF NOT EXISTS order_id TEXT NOT NULL DEFAULT '';
