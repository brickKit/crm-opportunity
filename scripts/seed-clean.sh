#!/usr/bin/env bash
# 撤销 seed.sh 灌的种子商机——反查 command_idempotency 拿真实行 id 精确
# 删除，不用名字模糊匹配。
#
# ⚠️ 只清本组件自己的 schema。`seed-opp-4` 赢单会真实触发 erp-sales 自动
# 建一张订单（事件握手，不是依赖边——本组件按 §1.4 铁律明确不依赖
# erp-sales，也不该知道怎么清理它的表）。那张联带订单的清理归装配层
# `infra/seed-data/clean.sh`，不在这里。
set -euo pipefail
C_GRN=$'\033[32m'; C_OFF=$'\033[0m'
ok()  { echo "${C_GRN}✓${C_OFF} $*"; }

docker exec -i be-postgres psql -U postgres -d brickkit_db -v ON_ERROR_STOP=1 -q <<'SQL'
SET search_path TO crm_opportunity;
DO $$
DECLARE
  opp_id text;
  opp_ids text[] := ARRAY[]::text[];
  k text;
BEGIN
  FOREACH k IN ARRAY ARRAY['seed-opp-1','seed-opp-2','seed-opp-3','seed-opp-4','seed-opp-5'] LOOP
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
ok "crm-opportunity 种子数据已清空"
