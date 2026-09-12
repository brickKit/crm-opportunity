#!/usr/bin/env bash
# 撤销 seed.sh 灌的种子商机——反查 command_idempotency 拿真实行 id 精确
# 删除，不用名字模糊匹配。
#
# ⚠️ 本次重做把"清理 erp-sales 联带自动建的订单"这段逻辑从装配层
# `infra/seed-data/clean.sh` 挪了过来（总纲 SOP-W-7 待办）：赢单转订单
# 虽然是事件握手不是同步依赖（§1.4 铁律），但"清理我导致产生的联带
# 数据"这件事本身没有第三方能比本组件自己更精确地知道该删哪几行——
# 本组件知道自己种了哪几个 WON 商机、也知道 erp-sales 消费
# crm.opportunity.won.v1 时用的幂等键固定是 `crm-won:<opportunity_id>`
# （backend/internal/tcc/opportunity_won.go 的既有约定，跨组件事件
# payload 的 opportunity_id 就是这个值），比装配层脚本原来"按种子客户
# id 反查所有订单"的做法更精确（那种做法会把这几个客户名下所有订单都
# 清掉，不管是不是这次 seed 建的）。
set -euo pipefail
C_GRN=$'\033[32m'; C_OFF=$'\033[0m'
ok()  { echo "${C_GRN}✓${C_OFF} $*"; }

psqlx() { docker exec -i be-postgres psql -U postgres -d brickkit_db -v ON_ERROR_STOP=1 "$@"; }

# ── 先反查两个 WON 商机（seed-opp-4/seed-opp-9）的真实 id，再反查
# erp-sales 那边对应的订单 id——必须在下面①清空 crm_opportunity 自己
# 的 command_idempotency 之前做，不然这两个 id 就再也查不到了。
OPP4_ID="$(psqlx -tA -q -c "SET search_path TO crm_opportunity; SELECT result_id FROM command_idempotency WHERE idempotency_key = 'seed-opp-4';")"
OPP9_ID="$(psqlx -tA -q -c "SET search_path TO crm_opportunity; SELECT result_id FROM command_idempotency WHERE idempotency_key = 'seed-opp-9';")"
OPP11_ID="$(psqlx -tA -q -c "SET search_path TO crm_opportunity; SELECT result_id FROM command_idempotency WHERE idempotency_key = 'seed-opp-11';")"

echo "── ① erp-sales：精确清理 WON 商机联带自动建的订单（不是按客户 id 模糊扫）──"
# ⚠️ seed-opp-11 是刻意会赢单失败的商机（信用超限）——订单会真的建出
# 来（DRAFT，CreateOrder 先于信用校验），但 FinalizeConfirm 那步真的
# 失败，同时真的建了一条 infra-workflow 异常待办（本组件不清理，那是
# 另一个组件的表，且是真实业务失败留下的历史记录，同"冗余可以接受"
# 判据——不是错误状态需要擦掉）。这里只清理 erp-sales 那张联带的 DRAFT
# 订单，跟 4/9 号走的是同一段代码。
for opp_id in "$OPP4_ID" "$OPP9_ID" "$OPP11_ID"; do
  [ -n "$opp_id" ] || continue
  order_id="$(psqlx -tA -q -c "SET search_path TO erp_sales; SELECT result_id FROM command_idempotency WHERE idempotency_key = 'crm-won:$opp_id';")"
  if [ -n "$order_id" ]; then
    psqlx -q -c "
      SET search_path TO erp_sales;
      DELETE FROM sales_order_items WHERE order_id = $order_id;
      DELETE FROM sales_orders WHERE id = $order_id;
      DELETE FROM command_idempotency WHERE idempotency_key IN ('crm-won:$opp_id', 'crm-won:$opp_id:confirm');
    "
    ok "已删除商机 $opp_id 联带的 erp-sales 订单 $order_id"
  fi
done

echo "── ② crm_opportunity：清空本组件自己的种子商机（1-10 号）──"
psqlx -q <<'SQL'
SET search_path TO crm_opportunity;
DO $$
DECLARE
  opp_id text;
  opp_ids text[] := ARRAY[]::text[];
  k text;
BEGIN
  FOREACH k IN ARRAY ARRAY[
    'seed-opp-1','seed-opp-2','seed-opp-3','seed-opp-4','seed-opp-5',
    'seed-opp-6','seed-opp-7','seed-opp-8','seed-opp-9','seed-opp-10',
    'seed-opp-11'
  ] LOOP
    SELECT result_id INTO opp_id FROM command_idempotency WHERE idempotency_key = k;
    IF opp_id IS NOT NULL AND opp_id <> '' THEN
      opp_ids := array_append(opp_ids, opp_id);
    END IF;
  END LOOP;
  IF array_length(opp_ids, 1) > 0 THEN
    DELETE FROM opportunity_stage_history WHERE opportunity_id::text = ANY(opp_ids);
    DELETE FROM opportunity_items WHERE opportunity_id::text = ANY(opp_ids);
    DELETE FROM opportunities WHERE id::text = ANY(opp_ids);
    DELETE FROM command_idempotency WHERE idempotency_key LIKE 'seed-opp-%';
    RAISE NOTICE '删除商机: %', opp_ids;
  END IF;
END $$;
SQL
ok "crm-opportunity 种子数据已清空（含联带的 erp-sales 订单）"
