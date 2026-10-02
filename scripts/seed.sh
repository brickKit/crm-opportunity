#!/usr/bin/env bash
# 灌本组件自己的种子商机，全部经真实 REST 接口建（幂等键 seed-opp-<n>，重跑
# 时重放，不重复建）：
#
# ① seed-opp-1..5：dev.superuser 的身份，3 条 OPEN 分处不同阶段 + 1 WON +
#    1 LOST。
# ② seed-opp-6..10：dev.sales.east（华东分部，dev_sales_rep 角色）的身份，
#    同样的状态谱系。dept_id/dept_path/owner_id 是创建时从调用者的
#    besdk.ScopeOf(ctx) 快照的，必须真的用这个身份的 JWT 去调接口，不能建完
#    再补写归属字段。这是"华东销售看不到别的部门商机"的演示样本。
#
# ⚠️ seed-opp-4 与 seed-opp-9 是两条 WON 商机：erp/sales 在闭包里时，赢单真实触发
# 它自动建单确认，seed-opp-9 的订单归属是 dev.sales.east/华东，演示"赢单转订单，
# 归属跟着走"。erp/sales 不在时商机照样 WON，只是没有订单；ok 行按回填的 order_id
# 如实说是哪一种。
#
# ⚠️ seed-opp-11：唯一一条刻意会赢单失败的商机——用信用额度只有 ¥5000 的
# 客户下一张远超额度的单，erp/sales 真实拒绝确认，真实建一条 infra/workflow
# 异常待办（见下方该商机定义处的注释）。
#
# ⚠️ 前置条件（本脚本自己不建，靠 Makefile 的 seed 目标链式调用）：
#   - infra-iam-casdoor 的种子身份（dev.superuser + dev.sales.east +
#     ROPC 测试应用）
#   - infra-authz 授予 dev.superuser 全权限键 + dev.sales.east 归华东
#     分部 + dev_sales_rep 角色（含 crm.opportunity.view/edit/win）
#   - mdm-customer/mdm-product 各自的种子客户/产品
# 单独跑 `make -C components/crm/opportunity seed` 就会把这些都建好，
# 不需要装配层编排。
#
# 用法：make -C components/crm/opportunity seed
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROOT="$(cd "$DIR/../../.." && pwd)"
source "$ROOT/infra/scripts/lib/seed-net.sh"
source "$DIR/scripts/schema.sh"
need python3; need docker
SCHEMA="$(schema_of crm/opportunity)"
CUSTOMER_SCHEMA="$(schema_of mdm/customer)"
PRODUCT_SCHEMA="$(schema_of mdm/product)"

seed_net_check
with_toolbox

CRM_REST="${CRM_REST:-http://$(service_name crm/opportunity):8102}"
check_healthz "$CRM_REST/healthz" "crm-opportunity"

wait_bundle_refresh

echo "── 换两个真实 JWT：dev.superuser（建 1-5）+ dev.sales.east（建 6-10，体现 org 维数据权限）──"
SUPERUSER_TOKEN="$(get_app_jwt dev.superuser)"
SALES_TOKEN="$(get_app_jwt dev.sales.east)"
ok "已换到真实应用 JWT（两个身份）"

CUST1="$(idfor "$CUSTOMER_SCHEMA" seed-customer-1)"
CUST2="$(idfor "$CUSTOMER_SCHEMA" seed-customer-2)"
CUST3="$(idfor "$CUSTOMER_SCHEMA" seed-customer-3)"
CUST4="$(idfor "$CUSTOMER_SCHEMA" seed-customer-4)"
CUST6="$(idfor "$CUSTOMER_SCHEMA" seed-customer-6)"
CUST7="$(idfor "$CUSTOMER_SCHEMA" seed-customer-7)"
CUST8="$(idfor "$CUSTOMER_SCHEMA" seed-customer-8)"
CUST9="$(idfor "$CUSTOMER_SCHEMA" seed-customer-9)"
PROD1="$(idfor "$PRODUCT_SCHEMA" seed-product-1)"
PROD2="$(idfor "$PRODUCT_SCHEMA" seed-product-2)"
PROD3="$(idfor "$PRODUCT_SCHEMA" seed-product-3)"
PROD4="$(idfor "$PRODUCT_SCHEMA" seed-product-4)"
PROD6="$(idfor "$PRODUCT_SCHEMA" seed-product-6)"
PROD7="$(idfor "$PRODUCT_SCHEMA" seed-product-7)"
PROD8="$(idfor "$PRODUCT_SCHEMA" seed-product-8)"
PROD9="$(idfor "$PRODUCT_SCHEMA" seed-product-9)"
PROD10="$(idfor "$PRODUCT_SCHEMA" seed-product-10)"
[ -n "$CUST1" ] && [ -n "$CUST6" ] || die "拿不到 mdm-customer 的种子客户 id——先确认 make -C ../../mdm/customer seed 跑成功了（要 1-9 号）"
[ -n "$PROD1" ] && [ -n "$PROD6" ] || die "拿不到 mdm-product 的种子产品 id——先确认 make -C ../../mdm/product seed 跑成功了（要 1-10 号）"

mkopportunity() { # token key name cust prod qty price amount -> id
  curl -s -X POST "$CRM_REST/crm/opportunity/opportunities" \
    -H "Authorization: Bearer $1" -H "Content-Type: application/json" \
    -d "{\"idempotency_key\":\"$2\",\"name\":\"$3\",\"customer_id\":\"$4\",\"items\":[{\"product_id\":\"$5\",\"qty\":\"$6\",\"quoted_unit_price\":\"$7\"}],\"expected_amount\":\"$8\"}" \
    | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])'
}
stage_opportunity() { # token id key stage
  curl -s -X POST "$CRM_REST/crm/opportunity/opportunities/$2/stage" \
    -H "Authorization: Bearer $1" -H "Content-Type: application/json" \
    -d "{\"idempotency_key\":\"$3\",\"to_stage_id\":\"$4\"}" >/dev/null
}
win_opportunity() { # token id key
  curl -s -X POST "$CRM_REST/crm/opportunity/opportunities/$2/win" \
    -H "Authorization: Bearer $1" -H "Content-Type: application/json" \
    -d "{\"idempotency_key\":\"$3\"}" >/dev/null
}
lose_opportunity() { # token id key reason
  curl -s -X POST "$CRM_REST/crm/opportunity/opportunities/$2/lose" \
    -H "Authorization: Bearer $1" -H "Content-Type: application/json" \
    -d "{\"idempotency_key\":\"$3\",\"reason\":\"$4\"}" >/dev/null
}

# 赢单转订单是事件握手：erp/sales 消费赢单事件建单、确认，确认时发
# sales.order.created.v1，本组件再消费它回填 order_id。只有看到 order_id 才说"转了
# 订单"——闭包里没有 erp/sales 时它永远不会出现，确认被拒的订单也不会回填。
# 第一条赢单最多等 WON_WAIT 秒；那一条等不到，就当 erp/sales 不在，后面的只看一眼。
WON_WAIT="${WON_WAIT:-20}"
order_of() { # token id -> 设 ORDER_ID（没有就空）。不在 $(…) 里调：要改 WON_WAIT
  local i
  ORDER_ID=""
  for ((i = 0; i <= WON_WAIT; i++)); do
    ORDER_ID="$(curl -s "$CRM_REST/crm/opportunity/opportunities/$2" -H "Authorization: Bearer $1" \
      | python3 -c 'import json,sys
try: print(json.load(sys.stdin).get("order_id") or "")
except Exception: print("")' || true)"
    [ -n "$ORDER_ID" ] && return 0
    [ "$i" -lt "$WON_WAIT" ] && sleep 1
  done
  WON_WAIT=0
}
won_label() { # order_id -> ok 行里这条赢单的说明
  if [ -n "$1" ]; then echo "WON→订单 $1"; else echo "WON，没有转出订单"; fi
}

echo "── ① dev.superuser：5 条示例商机（3 OPEN + 1 WON + 1 LOST，原样保留）──"
OPP1="$(mkopportunity "$SUPERUSER_TOKEN" seed-opp-1 "「本地测试」华南电子·Q1 采购意向" "$CUST1" "$PROD1" 10 100.00 "5000.00")"
OPP2="$(mkopportunity "$SUPERUSER_TOKEN" seed-opp-2 "「本地测试」京城机械·设备更新项目" "$CUST2" "$PROD2" 10 100.00 "12000.00")"
stage_opportunity "$SUPERUSER_TOKEN" "$OPP2" seed-opp-2-stage 3
OPP3="$(mkopportunity "$SUPERUSER_TOKEN" seed-opp-3 "「本地测试」江南纺织·年度框架合同" "$CUST3" "$PROD3" 10 100.00 "35000.00")"
stage_opportunity "$SUPERUSER_TOKEN" "$OPP3" seed-opp-3-stage 4
OPP4="$(mkopportunity "$SUPERUSER_TOKEN" seed-opp-4 "「本地测试」西部矿业·已成交项目" "$CUST4" "$PROD4" 10 100.00 "88000.00")"
win_opportunity "$SUPERUSER_TOKEN" "$OPP4" seed-opp-4-win
# ⚠️ 第 5 个客户/产品在各自组件的 seed 里是 DISABLED 样例（覆盖状态
# 完整度用的），CreateOpportunity 会拒绝非 ACTIVE 的客户/产品
# （service.go 真实校验），所以这里改回用 CUST1/PROD2——纯粹是拿一对
# 已知有效的组合，跟这条商机本身讲的是哪家客户没有关系。
OPP5="$(mkopportunity "$SUPERUSER_TOKEN" seed-opp-5 "「本地测试」滨海物流·已流失项目" "$CUST1" "$PROD2" 10 100.00 "15000.00")"
lose_opportunity "$SUPERUSER_TOKEN" "$OPP5" seed-opp-5-lose "「本地测试」价格谈不拢"
order_of "$SUPERUSER_TOKEN" "$OPP4"; ORDER4="$ORDER_ID"
ok "商机（dev.superuser）：$OPP1(OPEN) $OPP2(PROPOSAL) $OPP3(NEGOTIATION) $OPP4($(won_label "$ORDER4")) $OPP5(LOST)"

echo "── ② dev.sales.east（华东分部）：5 条商机，同样覆盖 OPEN 三阶段/WON/LOST ──"
OPP6="$(mkopportunity "$SALES_TOKEN" seed-opp-6 "「本地测试」华东·都市零售连锁超市·补货意向" "$CUST6" "$PROD6" 50 0.20 "3000.00")"
OPP7="$(mkopportunity "$SALES_TOKEN" seed-opp-7 "「本地测试」华东·云帆软件科技·减速机采购方案" "$CUST7" "$PROD9" 8 1580.00 "120000.00")"
stage_opportunity "$SALES_TOKEN" "$OPP7" seed-opp-7-stage 3
OPP8="$(mkopportunity "$SALES_TOKEN" seed-opp-8 "「本地测试」华东·恒基建筑工程·变频器批量采购谈判" "$CUST9" "$PROD10" 12 2200.00 "250000.00")"
stage_opportunity "$SALES_TOKEN" "$OPP8" seed-opp-8-stage 4
OPP9="$(mkopportunity "$SALES_TOKEN" seed-opp-9 "「本地测试」华东·云帆软件科技·防锈涂料已成交项目" "$CUST7" "$PROD7" 15 68.00 "45000.00")"
win_opportunity "$SALES_TOKEN" "$OPP9" seed-opp-9-win
OPP10="$(mkopportunity "$SALES_TOKEN" seed-opp-10 "「本地测试」华东·鲜达食品饮料·已流失项目" "$CUST8" "$PROD8" 20 15.50 "8000.00")"
lose_opportunity "$SALES_TOKEN" "$OPP10" seed-opp-10-lose "「本地测试」华东：客户资金紧张，暂缓采购"
order_of "$SALES_TOKEN" "$OPP9"; ORDER9="$ORDER_ID"
ok "商机（dev.sales.east/华东）：$OPP6(OPEN) $OPP7(PROPOSAL) $OPP8(NEGOTIATION) $OPP9($(won_label "$ORDER9")) $OPP10(LOST)"
if [ -n "$ORDER4" ] || [ -n "$ORDER9" ]; then
  echo "   （赢单转出的订单归属跟着商机走：$OPP4 是 dev.superuser/总部（根部门 /1/），$OPP9 是 dev.sales.east/华东——前端订单列表看得到）"
else
  echo "   （没有订单回填：erp/sales 不在这次的闭包里，或没在 等待时间内建单。商机照样是 WON）"
fi

# ⚠️ seed-opp-11：本组件唯一一条刻意设计成"赢单会真的失败"的商机——
# infra-workflow 的 CreateTask/CloseTask/CancelTask 明确不暴露 REST
# （contracts/infra/workflow/v1/workflow.proto 顶部注释："人能创建待办
# 只有一种可能'人代表某个业务组件创建'，那等于给了一条绕过业务规则的
# 路"）。种子脚本因此不能也不该直接 grpcurl 伪造一条 CreateTask 命令去
# 给 infra-workflow"造数据"——那正是这条设计禁令想挡住的事。真正诚实
# 的做法是让一条真实业务流程真的失败，产生一条真实的异常待办。
# CUST8（「本地测试」鲜达食品饮料，见 mdm-customer 自己的 seed 注释）
# 信用额度只有 ¥5000，专门留着给这个场景用；这里用大额产品把订单金额
# 做到远超额度，赢单后 erp-sales 的信用校验会真的拒绝确认（erp/sales 的
# 信用超限补偿路径），createOpportunity
# ExceptionTask 会真的建一条 infra-workflow 待办，assignee 取事件里的
# owner_id/dept_path——正好也是"华东"，让 infra-workflow 不需要独立
# make seed 也有一条真实的、org/owner 维归属正确的异常待办可看。
#
# 实测踩坑：qty 太小凑不出超额度的真实订单——erp-sales 的定价引擎
# （backend/internal/repo/pricing.go）不认商机行的 quoted_unit_price，
# 是真的查 pricelist_items 按 PRODUCT>CATEGORY>GLOBAL 规则重算，种子
# 产品都没有专属定价规则，一律落到 GLOBAL 规则的固定单价，跟这里传的
# 报价快照数字无关。qty 要大到"qty × 那个固定单价"真的超过 5000 额度
# 才行，不能照抄别的商机的 qty。
OPP11="$(mkopportunity "$SALES_TOKEN" seed-opp-11 "「本地测试」华东·鲜达食品饮料·超额度已成交项目" "$CUST8" "$PROD10" 100 2200.00 "22000.00")"
win_opportunity "$SALES_TOKEN" "$OPP11" seed-opp-11-win
# 这一条预期不回填：只短等几秒，确认它真的没有被确认。
SALES_SEEN="${ORDER4}${ORDER9}"
[ -n "$SALES_SEEN" ] && WON_WAIT=5
order_of "$SALES_TOKEN" "$OPP11"; ORDER11="$ORDER_ID"
if [ -n "$ORDER11" ]; then
  ok "商机（dev.sales.east/华东）：$OPP11(WON→订单 $ORDER11 已确认——额度超限的样本没有按预期被拒，查 erp/sales 的额度)"
elif [ -n "$SALES_SEEN" ]; then
  ok "商机（dev.sales.east/华东）：$OPP11(WON，erp/sales 没有确认它的订单：额度超限样本，异常待办在 infra/workflow)"
else
  ok "商机（dev.sales.east/华东）：$OPP11(WON，没有转出订单：erp/sales 不在)"
fi

# ── 时间跨度回填：seed-opp-6..10 回填成"最近 4 周内逐步推进"的样子，
# 1-5 保持建档时间不动，列表里有新有旧。opportunities 表不分区，直接
# UPDATE 安全。用 now() - interval 写绝对值，不是 created_at - interval——
# 重跑脚本不会越跑越早。
psqlx -q <<SQL
SET search_path TO $SCHEMA;
UPDATE opportunities SET created_at = now() - interval '25 days', updated_at = now() - interval '25 days' WHERE id = $OPP6;
UPDATE opportunities SET created_at = now() - interval '18 days', updated_at = now() - interval '18 days' WHERE id = $OPP7;
UPDATE opportunities SET created_at = now() - interval '10 days', updated_at = now() - interval '10 days' WHERE id = $OPP8;
UPDATE opportunities SET created_at = now() - interval '5 days'  WHERE id = $OPP9;
UPDATE opportunities SET created_at = now() - interval '2 days'  WHERE id = $OPP10;
SQL
ok "已给 seed-opp-6..10 回填历史 created_at（最近 2-25 天内，逐步推进的时间线）"
